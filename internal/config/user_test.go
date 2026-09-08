package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// userConfigHome points the user-level config at a temp dir and returns the
// grove subdirectory LoadUser reads from.
func userConfigHome(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "grove")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(dir))
	return dir
}

// loadUserError asserts LoadUser failed with a *UserConfigError and returns it.
func loadUserError(t *testing.T) *UserConfigError {
	t.Helper()
	_, err := LoadUser()
	if err == nil {
		t.Fatal("LoadUser: expected an error")
	}
	var cfgErr *UserConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("LoadUser: expected *UserConfigError, got %T: %v", err, err)
	}
	return cfgErr
}

func TestLoadUserMissingFile(t *testing.T) {
	dir := userConfigHome(t)

	cfgErr := loadUserError(t)

	if cfgErr.Problem != "not found" {
		t.Errorf("problem = %q, want %q", cfgErr.Problem, "not found")
	}
	// With nothing on disk the error must still name the canonical path, since
	// that is where the user has to create the file.
	if want := filepath.Join(dir, "config.json"); cfgErr.Path != want {
		t.Errorf("path = %q, want %q", cfgErr.Path, want)
	}
}

func TestLoadUserInvalidJSON(t *testing.T) {
	dir := userConfigHome(t)
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{"hooks": {oops}}`)

	cfgErr := loadUserError(t)

	if cfgErr.Problem != "not valid JSON" {
		t.Errorf("problem = %q, want %q", cfgErr.Problem, "not valid JSON")
	}
	if cfgErr.Path != path {
		t.Errorf("path = %q, want %q", cfgErr.Path, path)
	}
	if cfgErr.Err == nil {
		t.Error("expected the underlying parse error to be carried")
	}
}

func TestLoadUserUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads mode-000 files")
	}
	dir := userConfigHome(t)
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{}`)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}

	cfgErr := loadUserError(t)

	if cfgErr.Problem != "not readable" {
		t.Errorf("problem = %q, want %q", cfgErr.Problem, "not readable")
	}
	if !errors.Is(cfgErr, os.ErrPermission) {
		t.Errorf("expected a permission error to be carried, got %v", cfgErr.Err)
	}
}

func TestLoadUserReadsGroveJSON(t *testing.T) {
	dir := userConfigHome(t)
	// The user-level file has the same shape as a project's grove.json, so that
	// name is accepted here too.
	writeFile(t, filepath.Join(dir, "grove.json"), `{"hooks": {"onOpen": [{"type": "tmux"}]}}`)

	cfg, err := LoadUser()
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}

	if hooks := cfg.OnOpen(); len(hooks) != 1 || hooks[0].Type != "tmux" {
		t.Errorf("onOpen = %+v, want the single tmux recipe from grove.json", hooks)
	}
}

func TestLoadUserPrefersConfigJSONOverGroveJSON(t *testing.T) {
	dir := userConfigHome(t)
	writeFile(t, filepath.Join(dir, "grove.jsonc"), `{"hooks": {"onOpen": [{"type": "tmux"}]}}`)
	writeFile(t, filepath.Join(dir, "config.json"), `{"hooks": {"onOpen": [{"type": "vscode-color-config"}]}}`)

	cfg, err := LoadUser()
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}

	if hooks := cfg.OnOpen(); len(hooks) != 1 || hooks[0].Type != "vscode-color-config" {
		t.Errorf("onOpen = %+v, want the recipe from the canonical config.json", hooks)
	}
}

func TestLoadUserReadsConfigJSONC(t *testing.T) {
	dir := userConfigHome(t)
	// config.jsonc wins over config.json and may carry comments.
	writeFile(t, filepath.Join(dir, "config.json"), `{"hooks": {"onOpen": [{"type": "tmux"}]}}`)
	writeFile(t, filepath.Join(dir, "config.jsonc"), `{
  // launch an editor
  "hooks": { "onOpen": [ { "type": "command", "command": "cursor $GROVE_DIR" } ] }
}`)

	cfg, err := LoadUser()
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}

	hooks := cfg.OnOpen()
	if len(hooks) != 1 || hooks[0].Type != "command" {
		t.Errorf("onOpen = %+v, want the single command recipe from config.jsonc", hooks)
	}
}
