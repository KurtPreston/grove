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

// In a remote/WSL session the `cursor`/`code` CLI forwards the open-folder
// request to the running window over VSCODE_IPC_HOOK_CLI (or VSCODE_CLIENT_*),
// and refuses to run without them ("Command is only available in WSL or inside
// a Visual Studio Code terminal."). Those must survive even though they share
// the VSCODE_ prefix with the launcher vars we strip — note VSCODE_IPC_HOOK
// (launcher) is dropped while VSCODE_IPC_HOOK_CLI (routing) is kept.
func TestSanitizeLaunchEnvKeepsCLIRoutingVars(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"VSCODE_PID=123",
		"VSCODE_IPC_HOOK=/tmp/x.sock",
		"VSCODE_IPC_HOOK_CLI=/tmp/cli.sock",
		"VSCODE_CLIENT_COMMAND=/mnt/c/code.exe",
		"VSCODE_CLIENT_COMMAND_CWD=/mnt/c/proj",
		"GROVE_DIR=/w",
	}
	got := sanitizeLaunchEnv(in)
	want := []string{
		"PATH=/usr/bin",
		"VSCODE_IPC_HOOK_CLI=/tmp/cli.sock",
		"VSCODE_CLIENT_COMMAND=/mnt/c/code.exe",
		"VSCODE_CLIENT_COMMAND_CWD=/mnt/c/proj",
		"GROVE_DIR=/w",
	}
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
