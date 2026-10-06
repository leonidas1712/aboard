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
	"strings"
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
	typ             byte
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
	t        *testing.T
	files    map[string][]byte // by path on the server
	cut      map[string]bool   // paths whose download stops halfway
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
		t: t, files: map[string][]byte{}, cut: map[string]bool{},
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
		body, ok := r.files[req.URL.Path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		if r.cut[req.URL.Path] {
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
	return fmt.Sprintf("aboard_%s_%s_%s.tar.gz", installVersion, runtime.GOOS, runtime.GOARCH)
}

// publish serves a release whose archive for this machine holds entries, with
// checksums.txt matching it, at both the version's and the latest release's paths.
func (r *installRelease) publish(entries []archiveEntry) {
	archive := tarGz(r.t, entries)
	sum := sha256.Sum256(archive)
	other := fmt.Sprintf("aboard_%s_plan9_mips.tar.gz", installVersion)
	checksums := fmt.Sprintf("%s  %s\n%s  %s\n", strings.Repeat("0", 64), other, hex.EncodeToString(sum[:]), r.archiveName())
	tag := "/releases/download/v" + installVersion + "/"
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
func (r *installRelease) existing() string {
	p := filepath.Join(r.installed(), "aboard")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("earlier aboard\n"), 0o755); err != nil { //nolint:gosec // stands in for an installed program
		r.t.Fatal(err)
	}
	return p
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
	delete(r.files, "/releases/latest/download/checksums.txt") // the version is given
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
			p := "/releases/download/v" + installVersion + "/" + r.archiveName()
			r.files[p] = tarGz(r.t, append(goodEntries(), archiveEntry{name: "extra", body: "x"}))
			return nil
		}, "doesn't match its checksum"},
		{"an interrupted download", func(r *installRelease) []string {
			r.cut["/releases/download/v"+installVersion+"/"+r.archiveName()] = true
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
