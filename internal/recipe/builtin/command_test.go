package builtin

import (
	"testing"
)

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSanitizeLaunchEnvStripsEditorVars(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"VSCODE_PID=123",
		"VSCODE_IPC_HOOK=/tmp/x.sock",
		"VSCODE_ESM_ENTRYPOINT=vs/workbench/api/node/extensionHostProcess",
		"ELECTRON_RUN_AS_NODE=1",
		"ELECTRON_NO_ATTACH_CONSOLE=1",
		"GROVE_DIR=/w",
		"HOME=/home/me",
	}
	got := sanitizeLaunchEnv(in)
	want := []string{"PATH=/usr/bin", "GROVE_DIR=/w", "HOME=/home/me"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// A bare key with no '=' (unusual, but os.Environ can surface it) is matched on
// the whole token and dropped when it is a launcher var, kept otherwise.
func TestSanitizeLaunchEnvHandlesKeyOnly(t *testing.T) {
	in := []string{"ELECTRON_RUN_AS_NODE", "SOMETHING_ELSE"}
	got := sanitizeLaunchEnv(in)
	want := []string{"SOMETHING_ELSE"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
