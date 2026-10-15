package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListDefaults(t *testing.T) {
	cfg := Defaults()
	if !cfg.ListStatusEnabled() {
		t.Error("status check should be on by default")
	}
	if got := cfg.ListStatusTimeout(); got != DefaultListStatusTimeout {
		t.Errorf("default timeout = %v, want %v", got, DefaultListStatusTimeout)
	}
}

func TestLoadListSettings(t *testing.T) {
	cases := []struct {
		json    string
		status  bool
		timeout time.Duration
	}{
		{`{"list": {"status": false}}`, false, DefaultListStatusTimeout},
		{`{"list": {"statusTimeout": 0.5}}`, true, 500 * time.Millisecond},
		{`{"list": {"statusTimeout": 0}}`, true, 0},
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

		// The getters reflect the file
		if got := cfg.ListStatusEnabled(); got != c.status {
			t.Errorf("%s: ListStatusEnabled = %v, want %v", c.json, got, c.status)
		}
		if got := cfg.ListStatusTimeout(); got != c.timeout {
			t.Errorf("%s: ListStatusTimeout = %v, want %v", c.json, got, c.timeout)
		}
	}
}
