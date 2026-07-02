// Package config loads grove's per-project configuration from a machine-local
// grove.json (or grove.jsonc, which additionally allows comments and trailing
// commas) that lives at the project root (beside the bare .base repo). It
// replaces the previous spread of GROVE_* environment variables and the
// .grove/bootstrap script: recipes and their settings are declared once, in one
// file, and validated against grove.schema.json (shipped in the repo root).
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"grove/examples"
	"grove/internal/ui"
)

// Filename is the plain-JSON config name grove looks for at the project root
// (the fallback used when no grove.jsonc is present).
const Filename = "grove.json"

// SeedFilename is the config file `grove clone` writes for a fresh project: a
// commented grove.jsonc.
const SeedFilename = "grove.jsonc"

// Config is the parsed grove.json. Top-level fields hold the few settings that
// are not specific to a single recipe; everything else lives on the per-recipe
// entries in Recipes.
type Config struct {
	// Copy lists untracked files copied from the default-branch worktree into
	// freshly created worktrees.
	Copy []string `json:"copy,omitempty"`
	// Recipes are run, in order, when a branch is opened.
	Recipes []RecipeConfig `json:"recipes,omitempty"`
	// Prune configures `grove prune` merge detection. When omitted, squash/rebase
	// detection is on and the forge PR check is off.
	Prune *PruneConfig `json:"prune,omitempty"`
}

// PruneConfig holds the settings `grove prune` uses to decide which worktrees'
// branches count as merged.
type PruneConfig struct {
	// DetectSquash enables patch-equivalence detection of squash/rebase merges
	// (branches whose tip is not an ancestor of origin/default). Defaults to true
	// when nil.
	DetectSquash *bool `json:"detectSquash,omitempty"`
	// Forge optionally consults a forge (via the gh CLI) for authoritative
	// merged-PR state.
	Forge *ForgeConfig `json:"forge,omitempty"`
}

// ForgeConfig configures the optional forge PR check used by `grove prune`.
type ForgeConfig struct {
	// Enabled turns on the gh-based merged-PR lookup. Requires gh on PATH and
	// authentication for the remote's host.
	Enabled bool `json:"enabled,omitempty"`
	// Repo overrides the auto-detected host/owner/repo passed to `gh --repo`.
	Repo string `json:"repo,omitempty"`
}

// SquashDetectionEnabled reports whether `grove prune` should use
// patch-equivalence to catch squash/rebase merges (the default).
func (c Config) SquashDetectionEnabled() bool {
	if c.Prune == nil || c.Prune.DetectSquash == nil {
		return true
	}
	return *c.Prune.DetectSquash
}

// ForgeEnabled reports whether `grove prune` should consult the forge for
// merged-PR state.
func (c Config) ForgeEnabled() bool {
	return c.Prune != nil && c.Prune.Forge != nil && c.Prune.Forge.Enabled
}

// ForgeRepo returns the configured host/owner/repo override for the forge check,
// or "" to auto-detect from the origin remote.
func (c Config) ForgeRepo() string {
	if c.Prune == nil || c.Prune.Forge == nil {
		return ""
	}
	return c.Prune.Forge.Repo
}

// RecipeConfig is one entry in the recipes array: a recipe Type plus the
// type-specific settings it needs. Fields not relevant to a given type are
// simply left unset.
type RecipeConfig struct {
	Type string `json:"type"`

	// Lifecycle gate shared by every recipe. Both default to true when unset
	// (nil): OnCreate lets a recipe run when a worktree is freshly created,
	// OnOpen lets it run on every open (create, reopen, and plain-folder
	// launch). Set OnOpen to false for one-time, create-only setup.
	OnCreate *bool `json:"onCreate,omitempty"`
	OnOpen   *bool `json:"onOpen,omitempty"`

	// webhook
	URL    string         `json:"url,omitempty"`
	Token  string         `json:"token,omitempty"`
	Params map[string]any `json:"params,omitempty"`

	// tmux
	Layout string `json:"layout,omitempty"`

	// command
	Command string `json:"command,omitempty"`
	Shell   string `json:"shell,omitempty"`
}

// Defaults returns the configuration used when no grove.json is present: a
// single tmux recipe and the conventional .env copy.
func Defaults() Config {
	return Config{
		Copy:    []string{".env"},
		Recipes: []RecipeConfig{{Type: "tmux"}},
	}
}

// Load reads the project config, preferring <projectDir>/grove.jsonc over
// grove.json (both accept comments and trailing commas). A missing file yields
// Defaults() with no error. An unreadable or invalid file is non-fatal: it
// returns Defaults() and the error so the caller can warn. On success the
// parsed config is validated (emitting warnings for obviously-misconfigured
// recipes) and returned, with omitted top-level keys falling back to their
// defaults.
func Load(projectDir string) (Config, error) {
	b, found, err := readConfig([]string{
		filepath.Join(projectDir, "grove.jsonc"),
		filepath.Join(projectDir, Filename),
	})
	if err != nil {
		return Defaults(), err
	}
	if !found {
		return Defaults(), nil
	}

	// Start from defaults so an omitted "copy"/"recipes" key keeps the
	// conventional behavior, while an explicit (even empty) value overrides it.
	cfg := Defaults()
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Defaults(), err
	}
	cfg.validate()
	return cfg, nil
}

// readConfig reads the first existing file among paths and returns its
// JSONC-stripped bytes. found is false when none of the paths exist; an
// existing-but-unreadable file is reported via err (found=true).
func readConfig(paths []string) (b []byte, found bool, err error) {
	for _, p := range paths {
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			if errors.Is(rerr, fs.ErrNotExist) {
				continue
			}
			return nil, true, rerr
		}
		return stripJSONC(raw), true, nil
	}
	return nil, false, nil
}

// userConfigDir returns grove's user-level config directory, honoring
// $XDG_CONFIG_HOME and falling back to ~/.config/grove.
func userConfigDir() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "grove"), nil
}

// UserConfigPath returns the canonical path to grove's user-level config, used
// by the launch flow when cwd is not inside a grove project (and in the
// "not configured" error message). LoadUser also accepts a config.jsonc sibling.
func UserConfigPath() (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadUser reads the user-level config, preferring config.jsonc over
// config.json (see UserConfigPath). Unlike Load, it does NOT fall back to
// Defaults(): outside a grove project there is no sensible implicit recipe, so
// a missing file yields found=false and the caller decides what to do. An
// unreadable or invalid file is reported via err (found=true).
func LoadUser() (cfg Config, found bool, err error) {
	dir, err := userConfigDir()
	if err != nil {
		return Config{}, false, err
	}
	b, found, err := readConfig([]string{
		filepath.Join(dir, "config.jsonc"),
		filepath.Join(dir, "config.json"),
	})
	if err != nil {
		return Config{}, true, err
	}
	if !found {
		return Config{}, false, nil
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, true, err
	}
	cfg.validate()
	return cfg, true, nil
}

// Seed writes a starter grove.jsonc at projectDir, but only if no config
// (grove.jsonc or grove.json) exists yet, so re-cloning or manual edits are
// never clobbered.
func Seed(projectDir string) error {
	for _, name := range []string{SeedFilename, Filename} {
		if _, err := os.Stat(filepath.Join(projectDir, name)); err == nil {
			return nil
		}
	}
	return os.WriteFile(filepath.Join(projectDir, SeedFilename), []byte(examples.Starter), 0o644)
}

// validate emits warnings for recipe entries that are missing the fields their
// type requires. It never fails: grove stays usable so a single bad entry does
// not block opening a branch.
func (c Config) validate() {
	for i, r := range c.Recipes {
		if r.Type == "" {
			ui.Warn("grove.json: recipes[" + strconv.Itoa(i) + "] is missing \"type\"; ignoring.")
			continue
		}
		switch r.Type {
		case "webhook":
			if r.URL == "" {
				ui.Warn("grove.json: webhook recipe is missing \"url\"; it will be skipped.")
			}
		case "command":
			if r.Command == "" {
				ui.Warn("grove.json: command recipe is missing \"command\"; it will be skipped.")
			}
		}
	}
}
