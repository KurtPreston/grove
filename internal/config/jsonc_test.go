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
	if len(cfg.Recipes) == 0 {
		t.Fatal("seeded config parsed to zero recipes")
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
