// Package tmux holds low-level tmux helpers shared by the tmux recipe and the
// list/prune/rm CLI commands. Model: one session per project, one window per
// worktree (named after the sanitized branch), one pane per layout entry.
package tmux

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
)

// Has reports whether tmux is on PATH.
func Has() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

func runQuiet(args ...string) error {
	return exec.Command("tmux", args...).Run()
}

func runTTY(args ...string) error {
	cmd := exec.Command("tmux", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func out(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	var b strings.Builder
	cmd.Stdout = &b
	err := cmd.Run()
	return b.String(), err
}

// SessionExists reports whether a session with the exact name exists.
func SessionExists(session string) bool {
	return runQuiet("has-session", "-t", "="+session) == nil
}

// WindowExists reports whether a window name is present in the session.
func WindowExists(session, win string) bool {
	o, err := out("list-windows", "-t", "="+session, "-F", "#{window_name}")
	if err != nil {
		return false
	}
	sc := bufio.NewScanner(strings.NewReader(o))
	for sc.Scan() {
		if sc.Text() == win {
			return true
		}
	}
	return false
}

// EnsureSession creates the detached project session with a placeholder window
// if it does not already exist.
func EnsureSession(session, dir string) {
	if SessionExists(session) {
		return
	}
	_ = runQuiet("new-session", "-d", "-s", session, "-c", dir, "-n", "__grove__")
}

// KillPlaceholder removes the throwaway placeholder window once a real worktree
// window exists.
func KillPlaceholder(session string) {
	if !WindowExists(session, "__grove__") {
		return
	}
	o, _ := out("list-windows", "-t", "="+session)
	count := len(strings.Split(strings.TrimRight(o, "\n"), "\n"))
	if count > 1 {
		_ = runQuiet("kill-window", "-t", session+":__grove__")
	}
}

// ApplyWindowColor sets the per-window user options consumed by the tmux.conf
// window-status format (@grove_bg / @grove_fg) plus the active pane border.
func ApplyWindowColor(session, win, hex, fg string) {
	_ = runQuiet("set-window-option", "-t", session+":"+win, "@grove_bg", hex)
	_ = runQuiet("set-window-option", "-t", session+":"+win, "@grove_fg", fg)
	_ = runQuiet("set-window-option", "-t", session+":"+win, "pane-active-border-style", "fg="+hex)
}

type pane struct {
	name string
	cmd  string
}

func parseLayout(layout string) []pane {
	var res []pane
	for _, part := range strings.Split(layout, ",") {
		if part == "" {
			continue
		}
		if i := strings.Index(part, "="); i >= 0 {
			res = append(res, pane{name: part[:i], cmd: part[i+1:]})
		} else {
			res = append(res, pane{name: part})
		}
	}
	return res
}

// BuildWorktreeWindow appends a window for a worktree with one pane per layout
// entry, laid out left-to-right, focusing the rightmost pane.
func BuildWorktreeWindow(session, win, dir, layout string) {
	// -a -t "$session:{last}" always inserts after the final window regardless of
	// which window is current (avoids "index N in use" when run from a worktree).
	_ = runQuiet("new-window", "-a", "-t", session+":{last}", "-n", win, "-c", dir)
	panes := parseLayout(layout)
	if len(panes) == 0 {
		return
	}
	first := true
	for _, p := range panes {
		if first {
			first = false
		} else {
			_ = runQuiet("split-window", "-t", session+":"+win, "-c", dir)
		}
		if p.cmd != "" {
			_ = runQuiet("send-keys", "-t", session+":"+win, p.cmd, "C-m")
		}
	}
	_ = runQuiet("select-layout", "-t", session+":"+win, "even-horizontal")
	if runQuiet("select-pane", "-t", session+":"+win+".{bottom-right}") != nil {
		_ = runQuiet("select-pane", "-t", session+":"+win+".{right}")
	}
}

// EnsureWorktreeWindow ensures the worktree window exists and (re)applies color.
func EnsureWorktreeWindow(session, win, dir, hex, fg, layout string) {
	if !WindowExists(session, win) {
		BuildWorktreeWindow(session, win, dir, layout)
	}
	ApplyWindowColor(session, win, hex, fg)
}

// KillWindow removes a worktree's window (best effort).
func KillWindow(session, win string) {
	_ = runQuiet("kill-window", "-t", session+":"+win)
}

// AttachOrSwitch selects the window and attaches (or switches if already inside
// tmux), handing the controlling terminal to tmux.
func AttachOrSwitch(session, win string) {
	if win != "" {
		_ = runQuiet("select-window", "-t", session+":"+win)
	}
	if os.Getenv("TMUX") != "" {
		_ = runTTY("switch-client", "-t", session)
	} else {
		_ = runTTY("attach", "-t", session)
	}
}
