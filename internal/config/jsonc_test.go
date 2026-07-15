package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStripJSONCCommentsAndTrailingCommas(t *testing.T) {
	src := []byte(`{
  // a line comment
  "url": "https://example.com//path", /* block comment */
  "list": [
    "a,b", // string containing a comma
    "c",   /* trailing element */
  ],
  "n": 1,
}`)
	got := stripJSONC(src)

	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("stripped JSONC did not parse: %v\n%s", err, got)
	}
	if m["url"] != "https://example.com//path" {
		t.Errorf("url with // was mangled: %q", m["url"])
	}
	list, ok := m["list"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("list = %#v", m["list"])
	}
	if list[0] != "a,b" {
		t.Errorf("string with comma was mangled: %q", list[0])
	}
	if n, _ := m["n"].(float64); n != 1 {
		t.Errorf("n = %v, want 1", m["n"])
	}
}

func TestStripJSONCPreservesEscapedQuotes(t *testing.T) {
	src := []byte(`{"s": "he said \"// not a comment\" ok", "x": 1,}`)
	got := stripJSONC(src)

	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("stripped JSONC did not parse: %v\n%s", err, got)
	}
	if m["s"] != `he said "// not a comment" ok` {
		t.Errorf("escaped-quote string was mangled: %q", m["s"])
	}
}

func TestSeedWritesParseableJSONC(t *testing.T) {
	dir := t.TempDir()
	if err := Seed(dir); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, SeedFilename)); err != nil {
		t.Fatalf("expected %s to be written: %v", SeedFilename, err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load of seeded config: %v", err)
	}
	if cfg.Hooks == nil || len(cfg.Hooks.BeforeCreateBranch)+len(cfg.Hooks.OnCreateWorktree)+len(cfg.Hooks.OnOpen) == 0 {
		t.Fatal("seeded config parsed to zero hooks")
	}

	// Re-seeding must not clobber an existing config (jsonc or json).
	if err := os.WriteFile(filepath.Join(dir, SeedFilename), []byte(`{"copy":["mine"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Seed(dir); err != nil {
		t.Fatalf("re-Seed: %v", err)
	}
	cfg, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Copy) != 1 || cfg.Copy[0] != "mine" {
		t.Errorf("Seed clobbered an existing config: copy=%#v", cfg.Copy)
	}
}

// TestLoadDefaultsHooksWhenOmitted verifies an omitted "hooks" key falls back
// to the conventional single-tmux-recipe default (onOpen bucket).
func TestLoadDefaultsHooksWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "grove.json"), []byte(`{"copy":["x"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Hooks == nil || len(cfg.Hooks.OnOpen) != 1 || cfg.Hooks.OnOpen[0].Type != "tmux" {
		t.Errorf("expected default tmux-only onOpen hook, got %#v", cfg.Hooks)
	}
	if len(cfg.Hooks.BeforeCreateBranch) != 0 || len(cfg.Hooks.OnCreateWorktree) != 0 {
		t.Errorf("expected empty beforeCreateBranch/onCreateWorktree by default, got %#v", cfg.Hooks)
	}
}

// TestLoadPartialHooksDoesNotMergeDefaults guards against a subtle
// encoding/json pitfall: unmarshaling into an existing non-nil *Hooks (as
// Defaults() would produce) reuses that struct instead of replacing it, which
// would leave the default tmux onOpen recipe present alongside a project's own
// explicit hooks. A partial "hooks" object must be respected exactly, with
// unset buckets empty.
func TestLoadPartialHooksDoesNotMergeDefaults(t *testing.T) {
	dir := t.TempDir()
	src := `{"hooks": {"beforeCreateBranch": [{"type": "command", "command": "check.sh"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "grove.json"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Hooks.BeforeCreateBranch) != 1 || cfg.Hooks.BeforeCreateBranch[0].Command != "check.sh" {
		t.Errorf("beforeCreateBranch = %#v, want the configured command entry", cfg.Hooks.BeforeCreateBranch)
	}
	if len(cfg.Hooks.OnOpen) != 0 {
		t.Errorf("onOpen = %#v, want empty (must not inherit the default tmux recipe)", cfg.Hooks.OnOpen)
	}
}

func TestLoadPrefersJSONC(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "grove.json"), []byte(`{"copy":["from-json"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	jsonc := "{\n  // prefer me\n  \"copy\": [\"from-jsonc\"],\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "grove.jsonc"), []byte(jsonc), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Copy) != 1 || cfg.Copy[0] != "from-jsonc" {
		t.Errorf("expected grove.jsonc to win, got copy=%#v", cfg.Copy)
	}
}
