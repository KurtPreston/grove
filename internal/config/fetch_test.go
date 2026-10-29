package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFetchSetting(t *testing.T) {
	cases := []struct {
		json string
		want bool
	}{
		{`{}`, true},
		{`{"fetch": true}`, true},
		{`{"fetch": false}`, false},
	}
	for _, c := range cases {
		// Load each config without unknown-field warnings
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "grove.json"), c.json)
		var cfg Config
		out := captureStderr(t, func() {
			var err error
			if cfg, err = Load(dir); err != nil {
				t.Fatalf("Load(%s): %v", c.json, err)
			}
		})
		if strings.Contains(out, "unknown field") {
			t.Errorf("%s: unexpected warning %q", c.json, out)
		}
		if got := cfg.FetchEnabled(); got != c.want {
			t.Errorf("%s: FetchEnabled = %v, want %v", c.json, got, c.want)
		}
	}
}
