package recipe

import (
	"reflect"
	"testing"

	"grove/internal/config"
)

// TestInteractiveOnlySkippedWithoutTerminal checks that interactiveOnly
// recipes run only when the context is interactive, in both runners.
func TestInteractiveOnlySkippedWithoutTerminal(t *testing.T) {
	// Register built-ins that record each run
	var ran []string
	Register("test-always", func(Context, config.RecipeConfig) error { ran = append(ran, "always"); return nil })
	Register("test-tty", func(Context, config.RecipeConfig) error { ran = append(ran, "tty"); return nil })
	recipes := []config.RecipeConfig{
		{Type: "test-always"},
		{Type: "test-tty", InteractiveOnly: true},
	}

	// Each runner should skip the interactiveOnly entry without a terminal
	for _, interactive := range []bool{false, true} {
		want := []string{"always"}
		if interactive {
			want = append(want, "tty")
		}
		ctx := Context{Interactive: interactive}

		ran = nil
		Run(recipes, ctx)
		if !reflect.DeepEqual(ran, want) {
			t.Errorf("Run(interactive=%v) ran %v, want %v", interactive, ran, want)
		}

		ran = nil
		if err := RunBefore(recipes, ctx); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(ran, want) {
			t.Errorf("RunBefore(interactive=%v) ran %v, want %v", interactive, ran, want)
		}
	}
}

// TestEnvExportsInteractive checks GROVE_INTERACTIVE reflects the context.
func TestEnvExportsInteractive(t *testing.T) {
	if !contains(Context{Interactive: true}.Env(), "GROVE_INTERACTIVE=1") {
		t.Error("interactive context should export GROVE_INTERACTIVE=1")
	}
	if !contains(Context{}.Env(), "GROVE_INTERACTIVE=") {
		t.Error("non-interactive context should export an empty GROVE_INTERACTIVE")
	}
}

// contains reports whether s holds v (slices.Contains needs Go 1.21; grove
// supports 1.18).
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
