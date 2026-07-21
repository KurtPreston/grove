package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns
// everything written to it. ui.Warn writes to os.Stderr, so this lets the
// tests assert on the warnings emitted while loading a config.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestLoadWarnsUnknownTopLevelField(t *testing.T) {
	dir := t.TempDir()
	// A top-level "recipes" array is the classic mistake: recipes belong under
	// "hooks". The lenient parse drops it silently, so grove must warn.
	writeFile(t, filepath.Join(dir, "grove.jsonc"), `{
  "recipes": [ { "type": "vscode-color-config" } ]
}`)

	out := captureStderr(t, func() {
		if _, err := Load(dir); err != nil {
			t.Fatalf("Load: %v", err)
		}
	})

	if !strings.Contains(out, `unknown field "recipes"`) {
		t.Errorf("expected unknown-field warning for \"recipes\", got: %q", out)
	}
	if !strings.Contains(out, "grove.jsonc") {
		t.Errorf("expected warning to name the source file, got: %q", out)
	}
}

func TestLoadWarnsUnknownRecipeField(t *testing.T) {
	dir := t.TempDir()
	// "sshHost" is not a recognized recipe field (host goes under "params").
	writeFile(t, filepath.Join(dir, "grove.json"), `{
  "hooks": { "onOpen": [ { "type": "webhook", "url": "x", "sshHost": "desktop" } ] }
}`)

	out := captureStderr(t, func() {
		if _, err := Load(dir); err != nil {
			t.Fatalf("Load: %v", err)
		}
	})

	if !strings.Contains(out, `unknown field "sshHost"`) {
		t.Errorf("expected unknown-field warning for \"sshHost\", got: %q", out)
	}
}

func TestLoadNoWarnForValidConfig(t *testing.T) {
	dir := t.TempDir()
	// A well-formed config, including a "$schema" reference, must not warn.
	writeFile(t, filepath.Join(dir, "grove.jsonc"), `{
  "$schema": "https://example.com/grove.schema.json",
  "copy": [".env"],
  "hooks": { "onOpen": [ { "type": "command", "command": "cursor $GROVE_DIR" } ] }
}`)

	out := captureStderr(t, func() {
		if _, err := Load(dir); err != nil {
			t.Fatalf("Load: %v", err)
		}
	})

	if strings.Contains(out, "unknown field") {
		t.Errorf("expected no unknown-field warning, got: %q", out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
