package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestExampleConfigsParse guards the shipped examples/*.jsonc: each must be
// valid JSONC that unmarshals into a Config with at least one recipe. This also
// covers the embedded starter (examples/grove.jsonc).
func TestExampleConfigsParse(t *testing.T) {
	glob := filepath.Join("..", "..", "examples", "*.jsonc")
	paths, err := filepath.Glob(glob)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no example configs matched %s", glob)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		var cfg Config
		if err := json.Unmarshal(stripJSONC(b), &cfg); err != nil {
			t.Errorf("%s: does not parse as JSONC config: %v", p, err)
			continue
		}
		if len(cfg.Recipes) == 0 {
			t.Errorf("%s: parsed to zero recipes", p)
		}
	}
}
