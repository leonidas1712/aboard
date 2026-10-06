//go:build e2e

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The install script against a release server on this machine, with an isolated HOME
// and a PATH holding only the tools it needs, so nothing reaches the network or the
// machine's own cosign. The real download, and cosign checking a real signature, are
// steps in e2e/RELEASE_CHECKLIST.md.

const (
	installVersion = "9.9.9"
	// The identity a real release's checksums are signed with, and its issuer.
	releaseIdentity = "https://github.com/leonidas1712/aboard/.github/workflows/release.yml@refs/tags/v" + installVersion
	githubIssuer    = "https://token.actions.githubusercontent.com"
)

// fakeCosign stands in for cosign verify-blob: it accepts only when the identity and
// issuer the script asks for are the ones the bundle was "signed" with
// (FAKE_COSIGN_SIGNER and FAKE_COSIGN_ISSUER), as the real one does.
const fakeCosign = `#!/bin/sh
identity="" issuer=""
while [ $# -gt 0 ]; do
	case "$1" in
	--certificate-identity) identity=$2; shift ;;
	--certificate-oidc-issuer) issuer=$2; shift ;;
	esac
	shift
done
echo "$identity $issuer" >>"$FAKE_COSIGN_LOG"
if [ "$identity" = "$FAKE_COSIGN_SIGNER" ] && [ "$issuer" = "$FAKE_COSIGN_ISSUER" ]; then
	echo "Verified OK"
	exit 0
fi
echo "Error: none of the expected identities matched what was in the certificate" >&2
exit 1
`

// archiveEntry is one entry of a fake release archive.
type archiveEntry struct {
	name, body, link string
	typ              byte
}

// fakeAboard is an aboard that only answers aboard version.
const fakeAboard = "#!/bin/sh\necho 'aboard " + installVersion + "'\n"

func goodEntries() []archiveEntry {
	return []archiveEntry{
		{name: "aboard", body: fakeAboard},
		{name: "aboard-launcher-herdr", body: "#!/bin/sh\necho herdr launcher\n"},
		{name: "LICENSE", body: "license\n"},
		{name: "README.md", body: "readme\n"},
	}
}

func tarGz(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: 0o755, Linkname: e.link}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// installRelease is a fake GitHub release server and an isolated machine to run the
// install script on.
type installRelease struct {
	t       *testing.T
	version string // the version publish serves
	// mu guards what the server serves and records, which the test changes while the
	// server's goroutines read it.
	mu       sync.Mutex
	files    map[string][]byte // by path on the server
	cut      map[string]bool   // paths whose download stops halfway
	asked    []string          // the paths requested, in order
	url      string
	home     string
	bin      string // the PATH's only folder of commands
	cosigned string // the fake cosign's log
	env      []string
}

func newInstallRelease(t *testing.T) *installRelease {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &installRelease{
		t: t, files: map[string][]byte{}, cut: map[string]bool{}, version: installVersion,
		home: filepath.Join(dir, "home"), bin: filepath.Join(dir, "bin"),
		cosigned: filepath.Join(dir, "cosign.log"),
	}
	for _, d := range []string{r.home, r.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Only the commands the script uses, linked from this machine's PATH.
	for _, name := range []string{
		"sh", "curl", "tar", "gzip", "awk", "grep", "cut", "head", "cat", "mkdir",
		"mktemp", "rm", "cp", "mv", "chmod", "basename", "uname", "sysctl",
		"sha256sum", "shasum", "perl",
	} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if err := os.Symlink(p, filepath.Join(r.bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	r.publish(goodEntries())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.asked = append(r.asked, req.URL.Path)
		body, ok := r.files[req.URL.Path]
		cut := r.cut[req.URL.Path]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		if cut {
			_, _ = w.Write(body[:len(body)/2]) // the connection then closes early
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	r.url = srv.URL + "/releases"
	r.env = []string{
		"HOME=" + r.home,
		"PATH=" + r.bin,
		"TMPDIR=" + dir,
		"ABOARD_DOWNLOAD_URL=" + r.url,
		"FAKE_COSIGN_LOG=" + r.cosigned,
		"FAKE_COSIGN_ISSUER=" + githubIssuer,
	}
	return r
}

func (r *installRelease) archiveName() string {
	return fmt.Sprintf("aboard_%s_%s_%s.tar.gz", r.version, runtime.GOOS, runtime.GOARCH)
}

// archivePath is where the server serves the archive for this machine.
func (r *installRelease) archivePath() string {
	return "/releases/download/v" + r.version + "/" + r.archiveName()
}

// requests returns the paths requested so far.
func (r *installRelease) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.asked)
}

// setFile serves body at path.
func (r *installRelease) setFile(path string, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[path] = body
}

// removeFile stops serving path.
func (r *installRelease) removeFile(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.files, path)
}

// cutFile makes the download of path stop halfway.
func (r *installRelease) cutFile(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cut[path] = true
}

// publish serves a release whose archive for this machine holds entries, with
// checksums.txt matching it, at both the version's and the latest release's paths.
func (r *installRelease) publish(entries []archiveEntry) {
	archive := tarGz(r.t, entries)
	sum := sha256.Sum256(archive)
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.files)
	other := fmt.Sprintf("aboard_%s_plan9_mips.tar.gz", r.version)
	checksums := fmt.Sprintf("%s  %s\n%s  %s\n", strings.Repeat("0", 64), other, hex.EncodeToString(sum[:]), r.archiveName())
	tag := "/releases/download/v" + r.version + "/"
	r.files[tag+r.archiveName()] = archive
	r.files[tag+"checksums.txt"] = []byte(checksums)
	r.files[tag+"checksums.txt.sigstore.json"] = []byte("{}\n")
	r.files["/releases/latest/download/checksums.txt"] = []byte(checksums)
}

// withCosign puts the fake cosign on the PATH, its bundle signed by signer.
func (r *installRelease) withCosign(signer string) {
	if err := os.WriteFile(filepath.Join(r.bin, "cosign"), []byte(fakeCosign), 0o755); err != nil { //nolint:gosec // a fake command the script runs
		r.t.Fatal(err)
	}
	r.env = append(r.env, "FAKE_COSIGN_SIGNER="+signer)
}

// run runs scripts/install.sh with extra environment, returning its output and exit code.
func (r *installRelease) run(env ...string) (string, int) {
	r.t.Helper()
	cmd := exec.Command(filepath.Join(r.bin, "sh"), filepath.Join("..", "scripts", "install.sh"))
	cmd.Env = append(append([]string{}, r.env...), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			r.t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

func (r *installRelease) installed() string { return filepath.Join(r.home, ".local", "bin") }

// existing puts an aboard from an earlier install in the install folder.
func (r *installRelease) existing() {
	p := filepath.Join(r.installed(), "aboard")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("earlier aboard\n"), 0o755); err != nil { //nolint:gosec // stands in for an installed program
		r.t.Fatal(err)
	}
}

// refused checks the script failed saying want, and left the earlier aboard and
// nothing else in the install folder.
func (r *installRelease) refused(out string, code int, want string) {
	r.t.Helper()
	if code != 1 || !strings.Contains(out, want) {
		r.t.Fatalf("exit %d, want 1 with %q:\n%s", code, want, out)
	}
	if b, err := os.ReadFile(filepath.Join(r.installed(), "aboard")); err != nil || string(b) != "earlier aboard\n" {
		r.t.Fatalf("the earlier aboard changed: %q, %v", b, err)
	}
	ents, err := os.ReadDir(r.installed())
	if err != nil {
		r.t.Fatal(err)
	}
	if len(ents) != 1 {
		r.t.Fatalf("the install folder holds %v, want only the earlier aboard", ents)
	}
}

func TestInstallScriptInstallsTheLatestRelease(t *testing.T) {
	t.Parallel()
	r := newInstallRelease(t)
	r.existing()
	out, code := r.run()
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, name := range []string{"aboard", "aboard-launcher-herdr"} {
		p := filepath.Join(r.installed(), name)
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
		if !strings.Contains(out, "Installed "+p) {
			t.Fatalf("no line for %s:\n%s", name, out)
		}
	}
	got, err := exec.Command(filepath.Join(r.installed(), "aboard"), "version").Output()
	if err != nil || string(got) != "aboard "+installVersion+"\n" {
		t.Fatalf("installed aboard version: %q %v", got, err)
	}
	// Only programs are installed, and no temporary file is left.
	if ents, _ := os.ReadDir(r.installed()); len(ents) != 2 {
		t.Fatalf("install folder: %v", ents)
	}
	for _, want := range []string{
		"Downloading aboard " + installVersion + " for " + runtime.GOOS + "/" + runtime.GOARCH,
		"cosign isn't installed, so the checksums' signature wasn't checked.",
		"--certificate-identity " + releaseIdentity + " --certificate-oidc-issuer " + githubIssuer,
		r.installed() + " isn't on your PATH.",
		"aboard " + installVersion + " is installed. Next, set up your harnesses: " + filepath.Join(r.installed(), "aboard") + " init",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestInstallScriptInstallsAVersionIntoAFolder(t *testing.T) {
	t.Parallel()
	r := newInstallRelease(t)
	r.removeFile("/releases/latest/download/checksums.txt") // the version is given
	dir := filepath.Join(r.home, "tools")
	out, code := r.run("ABOARD_VERSION=v"+installVersion, "ABOARD_INSTALL_DIR="+dir, "PATH="+r.bin+":"+dir)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "aboard")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "isn't on your PATH") || !strings.HasSuffix(out, "Next, set up your harnesses: aboard init\n") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestInstallScriptChecksTheSignatureWithCosign(t *testing.T) {
	t.Parallel()
	r := newInstallRelease(t)
	r.withCosign(releaseIdentity)
	out, code := r.run()
	if code != 0 || !strings.Contains(out, "Checked the signature: signed by aboard's release workflow for v"+installVersion) {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if b, _ := os.ReadFile(r.cosigned); string(b) != releaseIdentity+" "+githubIssuer+"\n" {
		t.Fatalf("cosign was asked for %q", b)
	}
}

func TestInstallScriptRefuses(t *testing.T) {
	t.Parallel()
	notSigned := "the checksums' signature doesn't check out"
	notPlain := "holds something other than plain files"
	for _, tc := range []struct {
		name  string
		setup func(r *installRelease) []string
		want  string
	}{
		{"a signature from another repository's workflow", func(r *installRelease) []string {
			r.withCosign(strings.Replace(releaseIdentity, "leonidas1712/aboard", "someone/aboard", 1))
			return nil
		}, notSigned},
		{"a signature from a branch, not the tag", func(r *installRelease) []string {
			r.withCosign(strings.Replace(releaseIdentity, "refs/tags/v"+installVersion, "refs/heads/main", 1))
			return nil
		}, notSigned},
		{"a signature from another issuer", func(r *installRelease) []string {
			r.withCosign(releaseIdentity)
			return []string{"FAKE_COSIGN_ISSUER=https://accounts.example.test"}
		}, notSigned},
		{"a wrong checksum", func(r *installRelease) []string {
			r.setFile(r.archivePath(), tarGz(r.t, append(goodEntries(), archiveEntry{name: "extra", body: "x"})))
			return nil
		}, "doesn't match its checksum"},
		{"an interrupted download", func(r *installRelease) []string {
			r.cutFile(r.archivePath())
			return nil
		}, "couldn't download"},
		{"a path out of the folder", func(r *installRelease) []string {
			r.publish(append(goodEntries(), archiveEntry{name: "../escaped", body: "x"}))
			return nil
		}, notPlain},
		{"an absolute path", func(r *installRelease) []string {
			r.publish(append(goodEntries(), archiveEntry{name: "/tmp/escaped", body: "x"}))
			return nil
		}, notPlain},
		{"a symbolic link", func(r *installRelease) []string {
			entries := goodEntries()
			entries[1] = archiveEntry{name: "aboard-launcher-herdr", typ: tar.TypeSymlink, link: "/bin/sh"}
			r.publish(entries)
			return nil
		}, notPlain},
		{"a folder", func(r *installRelease) []string {
			r.publish(append(goodEntries(), archiveEntry{name: "sub/", typ: tar.TypeDir}))
			return nil
		}, notPlain},
		{"a file too large", func(r *installRelease) []string {
			// One byte over the 64 MiB a file in a release archive may have.
			r.publish(append(goodEntries(), archiveEntry{name: "padding", body: string(make([]byte, 64<<20+1))}))
			return nil
		}, "holds padding, larger than 67108864 bytes"},
		// With the limits lowered, small archives reach them.
		{"more than the total", func(r *installRelease) []string {
			r.publish(append(goodEntries(), archiveEntry{name: "a", body: strings.Repeat("x", 90)}, archiveEntry{name: "b", body: strings.Repeat("x", 90)}))
			return []string{"ABOARD_INSTALL_MAX_TOTAL=200"}
		}, "unpacks to more than 200 bytes"},
		{"a file too large and more than the total", func(r *installRelease) []string {
			entries := goodEntries()
			for i, n := range []int{90, 90, 90, 101} {
				entries = append(entries, archiveEntry{name: fmt.Sprintf("part-%d", i), body: strings.Repeat("x", n)})
			}
			r.publish(entries)
			return []string{"ABOARD_INSTALL_MAX_FILE=100", "ABOARD_INSTALL_MAX_TOTAL=200"}
		}, "holds part-3, larger than 100 bytes"},
		{"lowered limits can't be raised", func(r *installRelease) []string {
			r.publish(append(goodEntries(), archiveEntry{name: "padding", body: string(make([]byte, 64<<20+1))}))
			return []string{"ABOARD_INSTALL_MAX_FILE=999999999999", "ABOARD_INSTALL_MAX_TOTAL=999999999999"}
		}, "holds padding, larger than 67108864 bytes"},
		{"too many files", func(r *installRelease) []string {
			entries := goodEntries()
			for i := range 40 {
				entries = append(entries, archiveEntry{name: fmt.Sprintf("extra-%02d", i), body: "x"})
			}
			r.publish(entries)
			return nil
		}, "holds more than 32 files"},
		{"a plain-HTTP download address", func(r *installRelease) []string {
			return []string{"ABOARD_DOWNLOAD_URL=http://releases.example.test/releases"}
		}, "ABOARD_DOWNLOAD_URL must start with https://"},
		{"a version that isn't one", func(r *installRelease) []string {
			return []string{"ABOARD_VERSION=1.0/../../x"}
		}, "ABOARD_VERSION must be a version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newInstallRelease(t)
			r.t = t
			r.existing()
			env := tc.setup(r)
			out, code := r.run(env...)
			r.refused(out, code, tc.want)
			if _, err := os.Stat(filepath.Join(filepath.Dir(r.home), "escaped")); err == nil {
				t.Fatal("the archive wrote outside the folder")
			}
		})
	}
}

// Every launcher in launchers/ is built into the release archives, so the install
// script installs it next to aboard.
func TestReleaseBuildsEveryLauncher(t *testing.T) {
	t.Parallel()
	config, err := os.ReadFile(filepath.Join("..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := os.ReadDir(filepath.Join("..", "launchers"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		for _, want := range []string{"main: ./launchers/" + d.Name() + "\n", "binary: aboard-launcher-" + d.Name() + "\n"} {
			if !bytes.Contains(config, []byte(want)) {
				t.Errorf(".goreleaser.yaml has no %q: add a build for launchers/%s", strings.TrimSpace(want), d.Name())
			}
		}
	}
}

// scripts/release-notes prints a version's section of the changelog, the Unreleased
// section for a prerelease without one, and refuses a release without a section.
func TestReleaseNotes(t *testing.T) {
	t.Parallel()
	changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\n## Unreleased\n\n- Next\n\n## 0.2.0\n\n- Two\n\n## [0.1.0]\n\n- One\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ tag, want string }{
		{"v0.2.0", "\n- Two\n"},
		{"v0.1.0", "\n- One\n"},
		{"v0.3.0-rc.1", "\n- Next\n"},
		{"v0.2.0-rc.1", "\n- Next\n"},
		{"v0.3.0", ""},
	} {
		out, err := exec.Command(filepath.Join("..", "scripts", "release-notes"), tc.tag, changelog).Output()
		if got := string(out); got != tc.want || (err != nil) != (tc.want == "") {
			t.Errorf("%s: got %q (%v), want %q", tc.tag, got, err, tc.want)
		}
	}
}
