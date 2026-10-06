package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Releases are published on GitHub by the release workflow, the same files the install
// script (scripts/install.sh) reads: per version, aboard_<version>_<os>_<arch>.tar.gz,
// checksums.txt and its cosign bundle checksums.txt.sigstore.json. This file reads and
// checks them for aboard upgrade and the update notice, by the same rules as the script.

const (
	// releaseRepo is the repository that publishes aboard's releases.
	releaseRepo = "leonidas1712/aboard"
	// releaseIssuer issues the certificate a release's checksums are signed with:
	// GitHub's OIDC provider, for the release workflow.
	releaseIssuer = "https://token.actions.githubusercontent.com"
	// maxReleaseFile bounds any one download, well above an archive's size.
	maxReleaseFile = 256 << 20
	// maxArchiveEntry, maxArchiveTotal and maxArchiveEntries bound what a release archive
	// may unpack to: a file, all files together, and how many. A release holds aboard
	// (about 20 MiB), a few launchers and two text files.
	maxArchiveEntry   = 64 << 20
	maxArchiveTotal   = 128 << 20
	maxArchiveEntries = 32
)

// releaseIdentity is the signer a version's checksums must carry: the release workflow
// on that version's tag.
func releaseIdentity(v string) string {
	return "https://github.com/" + releaseRepo + "/.github/workflows/release.yml@refs/tags/v" + v
}

// versionText matches what a version may contain, so it is safe in a URL path.
var versionText = regexp.MustCompile(`^[0-9A-Za-z.+-]+$`)

// releases is where releases are downloaded from.
type releases struct {
	base   string
	client *http.Client
}

// releaseSource returns where releases are downloaded from: GitHub, or
// ABOARD_DOWNLOAD_URL, which must be https unless it is a server on this machine.
func (a *app) releaseSource(client *http.Client) (releases, error) {
	base := strings.TrimRight(strings.TrimSpace(a.env.Getenv("ABOARD_DOWNLOAD_URL")), "/")
	if base == "" {
		base = "https://github.com/" + releaseRepo + "/releases"
	}
	u, err := url.Parse(base)
	local := err == nil && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")
	if err != nil || (u.Scheme != "https" && !local) || u.Host == "" {
		return releases{}, newError("invalid_request", "ABOARD_DOWNLOAD_URL must start with https:// (got "+base+").",
			"Unset ABOARD_DOWNLOAD_URL to download from GitHub, or set it to an https:// address.")
	}
	// A redirect may not leave https for http.
	if !local {
		inner := *client
		inner.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return errors.New("redirected to " + req.URL.Scheme)
			}
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return nil
		}
		client = &inner
	}
	return releases{base: base, client: client}, nil
}

// errNotFound means the release server has no such file.
var errNotFound = errors.New("not found")

// get downloads one file, all of it or an error.
func (r releases) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+path, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%s answered %s", r.base+path, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxReleaseFile {
		return nil, fmt.Errorf("%s is larger than %d bytes", r.base+path, maxReleaseFile)
	}
	return data, nil
}

// archiveName is the release archive for this system.
func archiveName(v string) string {
	return "aboard_" + v + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
}

// latest returns the latest release's version, from the archive for this system named
// in its checksums.txt.
func (r releases) latest(ctx context.Context) (string, error) {
	data, err := r.get(ctx, "/latest/download/checksums.txt")
	if err != nil {
		return "", err
	}
	suffix := "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || !strings.HasPrefix(f[1], "aboard_") || !strings.HasSuffix(f[1], suffix) {
			continue
		}
		v := strings.TrimSuffix(strings.TrimPrefix(f[1], "aboard_"), suffix)
		if versionText.MatchString(v) {
			return v, nil
		}
	}
	return "", errNotFound
}

// checksumFor returns the SHA-256 checksums.txt gives for name, when it gives exactly one.
func checksumFor(checksums []byte, name string) (string, bool) {
	var found []string
	sc := bufio.NewScanner(bytes.NewReader(checksums))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == name {
			found = append(found, f[0])
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// downloaded is a release's archive for this system, checked.
type downloaded struct {
	// files are the archive's programs and other files, by name.
	files map[string][]byte
	// signatureChecked is true when cosign checked the checksums' signature.
	signatureChecked bool
}

// download fetches and checks version v's archive for this system: the checksums'
// signature when cosign is installed, the archive's checksum, and its contents. It
// writes nothing outside dir, a temporary folder of its own.
func (r releases) download(ctx context.Context, v, dir string) (downloaded, error) {
	var d downloaded
	tag := "/download/v" + v + "/"
	name := archiveName(v)
	checksums, err := r.get(ctx, tag+"checksums.txt")
	if errors.Is(err, errNotFound) {
		return d, newError("release_not_found", "There is no aboard release "+v+".",
			"Run aboard upgrade without --version for the latest release, or see https://github.com/"+releaseRepo+"/releases.")
	}
	if err != nil {
		return d, downloadError(err)
	}
	want, ok := checksumFor(checksums, name)
	if !ok {
		return d, newError("release_not_found", "aboard "+v+" has no build for "+runtime.GOOS+"/"+runtime.GOARCH+".",
			"See https://github.com/"+releaseRepo+"/releases for the builds there are.")
	}
	bundle, err := r.get(ctx, tag+"checksums.txt.sigstore.json")
	if err != nil {
		return d, downloadError(err)
	}
	archive, err := r.get(ctx, tag+name)
	if err != nil {
		return d, downloadError(err)
	}

	if cosign, err := exec.LookPath("cosign"); err == nil {
		sums, sig := filepath.Join(dir, "checksums.txt"), filepath.Join(dir, "checksums.txt.sigstore.json")
		if err := os.WriteFile(sums, checksums, 0o600); err != nil {
			return d, err
		}
		if err := os.WriteFile(sig, bundle, 0o600); err != nil {
			return d, err
		}
		cmd := exec.CommandContext(ctx, cosign, "verify-blob", "--bundle", sig, //nolint:gosec // cosign from the PATH, the person's own
			"--certificate-identity", releaseIdentity(v), "--certificate-oidc-issuer", releaseIssuer, sums)
		if out, err := cmd.CombinedOutput(); err != nil {
			return d, &Error{
				Code:    "signature_invalid",
				Message: "The checksums of aboard " + v + " weren't signed by aboard's release workflow for v" + v + ": " + strings.TrimSpace(string(out)),
				Hint:    "Nothing was changed. Don't install this download; report it at https://github.com/" + releaseRepo + "/issues.",
			}
		}
		d.signatureChecked = true
	}

	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return d, newError("checksum_mismatch",
			name+" doesn't match its checksum (got "+got+", want "+want+"): the download is incomplete or was changed.",
			"Nothing was changed; run aboard upgrade again.")
	}
	d.files, err = unpackArchive(archive)
	if err != nil {
		return d, newError("archive_invalid", name+" "+err.Error()+".", "Nothing was changed. Report it at https://github.com/"+releaseRepo+"/issues.")
	}
	return d, nil
}

func downloadError(err error) *Error {
	return &Error{
		Code: "download_failed", Message: "Couldn't download the release: " + err.Error() + ".",
		Hint: "Nothing was changed. Check your connection and run aboard upgrade again.", Err: err,
	}
}

// plainName is a file name with no folder, as release archives hold.
var plainName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// unpackArchive reads a release archive, which holds only plain files with plain names
// and an aboard among them; anything else (a folder, a link, a path) is refused, and so
// is an archive over the limits on one file's size, the total size and the file count.
// Nothing is cut short to fit.
func unpackArchive(archive []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, errors.New("isn't a readable archive")
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("isn't a readable archive")
		}
		if h.Typeflag != tar.TypeReg || !plainName.MatchString(h.Name) || files[h.Name] != nil {
			return nil, errors.New("holds something other than plain files (a folder, a link or a path)")
		}
		if len(files) == maxArchiveEntries {
			return nil, fmt.Errorf("holds more than %d files", maxArchiveEntries)
		}
		if h.Size > maxArchiveEntry {
			return nil, fmt.Errorf("holds %s, larger than %d MiB", h.Name, maxArchiveEntry>>20)
		}
		// Read one byte past the limit, so content longer than its header says is caught
		// rather than cut short.
		data, err := io.ReadAll(io.LimitReader(tr, maxArchiveEntry+1))
		if err != nil {
			return nil, errors.New("isn't a readable archive")
		}
		if len(data) > maxArchiveEntry {
			return nil, fmt.Errorf("holds %s, larger than %d MiB", h.Name, maxArchiveEntry>>20)
		}
		if total += int64(len(data)); total > maxArchiveTotal {
			return nil, fmt.Errorf("unpacks to more than %d MiB", maxArchiveTotal>>20)
		}
		files[h.Name] = data
	}
	if files["aboard"] == nil {
		return nil, errors.New("has no aboard program")
	}
	return files, nil
}
