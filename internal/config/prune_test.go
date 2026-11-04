package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPruneScope(t *testing.T) {
	for _, tc := range []struct {
		json string
		want string
	}{
		{`{}`, "all"},
		{`{"prune":{}}`, "all"},
		{`{"prune":{"scope":"local"}}`, "local"},
		{`{"prune":{"scope":"all"}}`, "all"},
	} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "grove.jsonc"), tc.json)
		out := captureStderr(t, func() {
			cfg, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.PruneScope(); got != tc.want {
				t.Errorf("PruneScope = %q, want %q", got, tc.want)
			}
		})
		if strings.Contains(out, "unknown field") {
			t.Errorf("unexpected warning: %s", out)
		}
	}
}
