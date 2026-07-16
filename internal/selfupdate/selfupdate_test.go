package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	tests := map[string]string{
		"v0.3.2": "0.3.2",
		"0.3.2":  "0.3.2",
		" v1.0 ": "1.0",
		"dev":    "dev",
		"":       "",
	}
	for in, want := range tests {
		if got := normalizeVersion(in); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSameVersion(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"0.3.2", "v0.3.2", true},  // baked-in vs tag: same release
		{"v0.3.2", "v0.3.2", true}, // identical tags
		{"0.3.2", "v0.3.3", false}, // different releases
		{"dev", "v0.3.2", false},   // source build is never "current"
		{"", "v0.3.2", false},      // unknown never matches
		{"", "", false},            // both empty is not a match
	}
	for _, tc := range tests {
		if got := sameVersion(tc.a, tc.b); got != tc.want {
			t.Errorf("sameVersion(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "abc123  grove_linux_amd64.tar.gz\n" +
		"def456 *grove_darwin_arm64.tar.gz\n" +
		"deadbeef  checksums.txt\n"
	if got := checksumFor(sums, "grove_linux_amd64.tar.gz"); got != "abc123" {
		t.Errorf("linux/amd64 = %q, want abc123", got)
	}
	if got := checksumFor(sums, "grove_darwin_arm64.tar.gz"); got != "def456" {
		t.Errorf("darwin/arm64 (binary marker) = %q, want def456", got)
	}
	if got := checksumFor(sums, "grove_windows_amd64.tar.gz"); got != "" {
		t.Errorf("missing entry = %q, want empty", got)
	}
}

func TestExtractArchive(t *testing.T) {
	bin := []byte("BINARY-BYTES")
	archive := makeArchive(t, map[string][]byte{
		"grove":            bin,
		"shell/grove.bash": []byte("bash"),
		"shell/grove.fish": []byte("fish"),
		"README.md":        []byte("readme"),
	})
	gotBin, shells, err := extractArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBin, bin) {
		t.Errorf("binary = %q, want %q", gotBin, bin)
	}
	if string(shells["grove.bash"]) != "bash" || string(shells["grove.fish"]) != "fish" {
		t.Errorf("shells = %v, want bash/fish content", shells)
	}
	if _, ok := shells["README.md"]; ok {
		t.Errorf("README.md should not be collected as a shell file")
	}
}

func TestExtractArchiveMissingBinary(t *testing.T) {
	archive := makeArchive(t, map[string][]byte{"README.md": []byte("readme")})
	if _, _, err := extractArchive(archive); err == nil {
		t.Fatal("expected an error when the archive has no grove binary")
	}
}

func TestReplaceFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "grove")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(target, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want \"new\"", got)
	}
	// No staging temp files should be left behind next to the target.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only the target to remain, found %d entries", len(entries))
	}
}

// TestClientRunInstalls drives the whole update flow against a fake GitHub:
// resolve the latest tag, download + verify the archive, swap the binary, and
// refresh only the shell scripts that were already installed.
func TestClientRunInstalls(t *testing.T) {
	newBin := []byte("NEW-GROVE-BINARY")
	archive := makeArchive(t, map[string][]byte{
		"grove":            newBin,
		"shell/grove.bash": []byte("new bash"),
		"shell/grove.fish": []byte("new fish"),
		"README.md":        []byte("readme"),
	})
	srv := releaseServer(t, "v9.9.9", archive, sha256hex(archive))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "grove")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	share := t.TempDir()
	t.Setenv("XDG_DATA_HOME", share)
	if err := os.MkdirAll(filepath.Join(share, "grove"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Only grove.bash pre-exists, proving we refresh installed files but never
	// create integration the user didn't opt into.
	if err := os.WriteFile(filepath.Join(share, "grove", "grove.bash"), []byte("old bash"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := testClient(srv, target)
	res, err := c.run(Options{CurrentVersion: "0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.UpToDate {
		t.Fatal("expected an update, got up-to-date")
	}
	if res.ToVersion != "v9.9.9" {
		t.Errorf("ToVersion = %q, want v9.9.9", res.ToVersion)
	}
	if got, _ := os.ReadFile(target); !bytes.Equal(got, newBin) {
		t.Errorf("binary = %q, want %q", got, newBin)
	}
	if !reflect.DeepEqual(res.ShellUpdated, []string{"grove.bash"}) {
		t.Errorf("ShellUpdated = %v, want [grove.bash]", res.ShellUpdated)
	}
	if got, _ := os.ReadFile(filepath.Join(share, "grove", "grove.bash")); string(got) != "new bash" {
		t.Errorf("grove.bash = %q, want \"new bash\"", got)
	}
	if _, err := os.Stat(filepath.Join(share, "grove", "grove.fish")); !os.IsNotExist(err) {
		t.Errorf("grove.fish should not have been created")
	}
}

func TestClientRunUpToDate(t *testing.T) {
	archive := makeArchive(t, map[string][]byte{"grove": []byte("x")})
	srv := releaseServer(t, "v9.9.9", archive, sha256hex(archive))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "grove")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := testClient(srv, target)
	res, err := c.run(Options{CurrentVersion: "9.9.9"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.UpToDate {
		t.Fatal("expected up-to-date")
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Errorf("binary was replaced despite being up to date: %q", got)
	}
}

func TestClientRunForceReinstalls(t *testing.T) {
	newBin := []byte("REINSTALLED")
	archive := makeArchive(t, map[string][]byte{"grove": newBin})
	srv := releaseServer(t, "v9.9.9", archive, sha256hex(archive))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "grove")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := testClient(srv, target)
	res, err := c.run(Options{CurrentVersion: "9.9.9", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.UpToDate {
		t.Fatal("force should reinstall even when current")
	}
	if got, _ := os.ReadFile(target); !bytes.Equal(got, newBin) {
		t.Errorf("binary = %q, want %q", got, newBin)
	}
}

func TestClientRunChecksumMismatch(t *testing.T) {
	archive := makeArchive(t, map[string][]byte{"grove": []byte("x")})
	srv := releaseServer(t, "v9.9.9", archive, "0000000000000000000000000000000000000000000000000000000000000000")
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "grove")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := testClient(srv, target)
	if _, err := c.run(Options{CurrentVersion: "0.0.1"}); err == nil {
		t.Fatal("expected a checksum-mismatch error")
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Errorf("binary must not be replaced on checksum mismatch: %q", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

const testRepo = "OWNER/REPO"

func testClient(srv *httptest.Server, target string) *client {
	return &client{
		repo:       testRepo,
		apiBase:    srv.URL,
		dlBase:     srv.URL,
		http:       srv.Client(),
		binaryPath: target,
	}
}

// releaseServer stands up a fake GitHub serving one release: the latest-release
// metadata, the platform archive, and its checksums.txt.
func releaseServer(t *testing.T, tag string, archive []byte, sum string) *httptest.Server {
	t.Helper()
	asset := assetName()
	routes := map[string][]byte{
		"/repos/" + testRepo + "/releases/latest":                       []byte(`{"tag_name":"` + tag + `"}`),
		"/" + testRepo + "/releases/download/" + tag + "/" + asset:      archive,
		"/" + testRepo + "/releases/download/" + tag + "/checksums.txt": []byte(sum + "  " + asset + "\n"),
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
}

func makeArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
