// Package selfupdate implements `grove update`: it replaces the running grove
// binary in place with the latest release published on GitHub.
//
// It mirrors the mechanics of install.sh (resolve the tag, download the
// per-platform archive, verify it against checksums.txt, unpack the binary and
// the shell integration) but does it natively so the command needs no curl|bash,
// can compare the running version against the release, and can report exactly
// what changed. The running binary is swapped atomically by writing the new one
// beside it and renaming over the top: on Unix that replaces the directory entry
// while the live process keeps its now-unlinked inode, sidestepping the ETXTBSY
// you would hit trying to open the running executable for writing.
package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"grove/internal/ui"
)

// DefaultRepo is the owner/repo grove updates from. GROVE_REPO overrides it,
// matching install.sh.
const DefaultRepo = "KurtPreston/grove"

// binaryName is the archive entry holding the grove executable; shellFiles are
// the shell-integration entries. Both mirror the layout produced by
// .goreleaser.yaml and consumed by install.sh.
const binaryName = "grove"

var shellFiles = []string{"grove.bash", "grove.fish", "grove-completion.bash", "grove-completion.zsh", "grove-completion.fish"}

// Base URLs are package vars (not consts) so tests can point the flow at an
// httptest server instead of the real GitHub.
var (
	defaultAPIBase      = "https://api.github.com"
	defaultDownloadBase = "https://github.com"
)

// Options configures an update run. The zero value updates the running binary to
// the latest release of DefaultRepo; env vars fill in anything left empty.
type Options struct {
	Repo           string // owner/repo; defaults to $GROVE_REPO or DefaultRepo
	CurrentVersion string // the running build's version (e.g. "0.3.2" or "dev")
	TargetVersion  string // release tag to install (e.g. "v0.3.3"); default $GROVE_VERSION or the latest release
	Force          bool   // reinstall even when already on the target version
}

// Result reports the outcome of a run so the caller can print a summary.
type Result struct {
	UpToDate     bool     // already on the target version; nothing was changed
	FromVersion  string   // the version that was running before the update
	ToVersion    string   // the release tag that was installed (or is current)
	BinaryPath   string   // the binary that was (or would be) replaced
	ShellUpdated []string // shell-integration files refreshed, if any
}

// Run resolves the target release and, unless we are already on it, downloads,
// verifies, and installs it over the running binary.
func Run(opts Options) (Result, error) {
	if opts.Repo == "" {
		opts.Repo = envOr("GROVE_REPO", DefaultRepo)
	}
	if opts.TargetVersion == "" {
		opts.TargetVersion = os.Getenv("GROVE_VERSION")
	}
	c := &client{
		repo:    opts.Repo,
		apiBase: defaultAPIBase,
		dlBase:  defaultDownloadBase,
		http:    &http.Client{Timeout: 5 * time.Minute},
	}
	return c.run(opts)
}

// client carries the resolved endpoints and HTTP client for one run. binaryPath
// overrides the auto-detected executable path (tests set it so a run never
// clobbers the test runner).
type client struct {
	repo       string
	apiBase    string
	dlBase     string
	http       *http.Client
	binaryPath string
}

func (c *client) run(opts Options) (Result, error) {
	res := Result{FromVersion: opts.CurrentVersion}

	tag := opts.TargetVersion
	if tag == "" {
		latest, err := c.latestTag()
		if err != nil {
			return res, err
		}
		tag = latest
	}
	res.ToVersion = tag

	if !opts.Force && sameVersion(opts.CurrentVersion, tag) {
		res.UpToDate = true
		return res, nil
	}

	if !supportedPlatform() {
		return res, fmt.Errorf("no prebuilt grove for %s (releases ship linux/darwin on amd64/arm64)", platform())
	}

	exe, err := c.targetBinary()
	if err != nil {
		return res, err
	}
	res.BinaryPath = exe

	archive := assetName()
	ui.Info(fmt.Sprintf("Downloading grove %s (%s)...", tag, platform()))
	data, err := c.fetchArchive(tag, archive)
	if err != nil {
		return res, err
	}
	if err := c.verify(tag, archive, data); err != nil {
		return res, err
	}

	bin, shells, err := extractArchive(data)
	if err != nil {
		return res, err
	}
	if err := replaceFile(exe, bin, 0o755); err != nil {
		return res, err
	}
	res.ShellUpdated = refreshShellScripts(shells)
	return res, nil
}

// latestTag returns the tag_name of the repo's latest GitHub release.
func (c *client) latestTag() (string, error) {
	url := c.apiBase + "/repos/" + c.repo + "/releases/latest"
	body, err := c.get(url, "application/vnd.github+json")
	if err != nil {
		return "", fmt.Errorf("could not resolve the latest release of %s (set GROVE_VERSION to pin one): %w", c.repo, err)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("could not parse the latest release of %s: %w", c.repo, err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("no tagged release found for %s (set GROVE_VERSION to pin one)", c.repo)
	}
	return rel.TagName, nil
}

// fetchArchive downloads the per-platform release archive for tag.
func (c *client) fetchArchive(tag, archive string) ([]byte, error) {
	data, err := c.get(c.releaseURL(tag, archive), "")
	if err != nil {
		return nil, fmt.Errorf("could not download %s for %s: %w", archive, tag, err)
	}
	return data, nil
}

// verify checks the archive against the release's checksums.txt. Verification is
// best effort (matching install.sh): a missing checksums file or entry warns and
// proceeds, but a present-and-mismatched checksum is fatal.
func (c *client) verify(tag, archive string, data []byte) error {
	sums, err := c.get(c.releaseURL(tag, "checksums.txt"), "")
	if err != nil {
		ui.Warn("no checksums.txt for " + tag + "; skipping verification.")
		return nil
	}
	want := checksumFor(string(sums), archive)
	if want == "" {
		ui.Warn("no checksum listed for " + archive + "; skipping verification.")
		return nil
	}
	got := sha256hex(data)
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s)", archive, want, got)
	}
	return nil
}

func (c *client) releaseURL(tag, file string) string {
	return c.dlBase + "/" + c.repo + "/releases/download/" + tag + "/" + file
}

// get performs a GET and returns the body, treating any non-200 as an error.
// GitHub rejects requests without a User-Agent, so one is always set.
func (c *client) get(url, accept string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "grove-selfupdate")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// targetBinary is the executable to replace: the injected path when set (tests),
// otherwise the running binary with symlinks resolved so we rewrite the real
// file rather than a link to it.
func (c *client) targetBinary() (string, error) {
	if c.binaryPath != "" {
		return c.binaryPath, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not locate the running grove binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// extractArchive unpacks the grove binary and any shell-integration files from a
// release tar.gz. Shell files are keyed by base name.
func extractArchive(data []byte) (binary []byte, shells map[string][]byte, err error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("could not open release archive: %w", err)
	}
	defer gz.Close()

	shells = map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("could not read release archive: %w", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		name := strings.TrimPrefix(filepath.ToSlash(hdr.Name), "./")
		switch {
		case name == binaryName:
			if binary, err = io.ReadAll(tr); err != nil {
				return nil, nil, fmt.Errorf("could not read grove binary from archive: %w", err)
			}
		case strings.HasPrefix(name, "shell/"):
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, nil, fmt.Errorf("could not read %s from archive: %w", name, err)
			}
			shells[filepath.Base(name)] = b
		}
	}
	if binary == nil {
		return nil, nil, fmt.Errorf("release archive did not contain a %q binary", binaryName)
	}
	return binary, shells, nil
}

// replaceFile atomically swaps target for data: write a sibling temp file, then
// rename over target. The sibling guarantees the rename stays on one filesystem,
// and renaming (rather than truncating in place) is what makes replacing a
// running executable safe on Unix.
func replaceFile(target string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".grove-update-*")
	if err != nil {
		return fmt.Errorf("cannot stage update in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("could not write update: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("could not write update: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("could not set update permissions: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("could not replace %s (check write permission on its directory): %w", target, err)
	}
	renamed = true
	return nil
}

// refreshShellScripts rewrites already-installed shell-integration files with the
// versions from the archive. It only touches files that already exist in the
// share dir, so it never creates integration the user didn't opt into; write
// failures warn but never fail the update (the binary is already swapped).
func refreshShellScripts(shells map[string][]byte) []string {
	dir := shareDir()
	if dir == "" {
		return nil
	}
	var updated []string
	for _, name := range shellFiles {
		content, ok := shells[name]
		if !ok {
			continue
		}
		dst := filepath.Join(dir, name)
		if _, err := os.Stat(dst); err != nil {
			continue
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			ui.Warn("could not update " + dst + ": " + err.Error())
			continue
		}
		updated = append(updated, name)
	}
	return updated
}

// shareDir is where install.sh drops the shell-integration scripts:
// $XDG_DATA_HOME/grove, falling back to ~/.local/share/grove.
func shareDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "grove")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "grove")
}

// checksumFor finds the sha256 for file in a `sha256  name` checksums list (the
// format sha256sum/goreleaser emit), tolerating the `*name` binary-mode marker.
func checksumFor(sums, file string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == file {
			return fields[0]
		}
	}
	return ""
}

func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// sameVersion reports whether two version strings name the same release,
// ignoring a leading "v" (releases tag "v0.3.2" while the baked-in version is
// "0.3.2"). Non-semver builds like "dev" never match a real tag.
func sameVersion(a, b string) bool {
	na, nb := normalizeVersion(a), normalizeVersion(b)
	return na != "" && na == nb
}

func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// assetName is the release archive for the running platform, matching the
// name_template in .goreleaser.yaml.
func assetName() string {
	return fmt.Sprintf("grove_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

func platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// supportedPlatform reports whether releases ship a build for this OS/arch.
func supportedPlatform() bool {
	osOK := runtime.GOOS == "linux" || runtime.GOOS == "darwin"
	archOK := runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64"
	return osOK && archOK
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
