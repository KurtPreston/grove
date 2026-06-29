// Package project is grove's data layer. git is the source of truth: the
// branch->folder mapping comes straight from `git worktree list`, so there is no
// separate state file to drift. A grove "project" is a directory containing a
// bare `.base` repo plus one worktree folder per branch.
package project

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"grove/internal/ui"
)

// Project locates the bare repo and the directory that holds it + the worktrees.
type Project struct {
	Base string // path to the bare repo (.base)
	Dir  string // parent dir holding .base and the worktree folders
}

// Name is the project's display name (the folder basename).
func (p *Project) Name() string { return filepath.Base(p.Dir) }

// ---------------------------------------------------------------------------
// git helpers (exported so the CLI layer can reuse them without re-shelling)
// ---------------------------------------------------------------------------

// Git runs `git -C dir args...`, streaming chatter to stderr.
func Git(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// GitQuiet runs `git -C dir args...` discarding all output; returns success.
func GitQuiet(dir string, args ...string) bool {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	return cmd.Run() == nil
}

// GitOut runs `git -C dir args...` and returns stdout (stderr discarded).
func GitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return out.String(), err
}

// GitPlain runs `git args...` with no -C (used by clone).
func GitPlain(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ---------------------------------------------------------------------------
// Resolution
// ---------------------------------------------------------------------------

// FindRoot walks up from start looking only for the grove project marker (a
// directory containing .base), returning that directory and true if found.
// Unlike Resolve, it does NOT consult git, so an ordinary git repo that is not
// a grove project reports false. This is what distinguishes a real grove
// worktree from any other directory for the launch fallback.
func FindRoot(start string) (string, bool) {
	dir := start
	for dir != "" && dir != "/" {
		if isDir(filepath.Join(dir, ".base")) {
			return dir, true
		}
		dir = filepath.Dir(dir)
	}
	return "", false
}

// Resolve walks up from start to find the grove project marker (a directory
// containing .base). Falls back to git's view for repos whose bare dir is named
// otherwise.
func Resolve(start string) (*Project, error) {
	if dir, ok := FindRoot(start); ok {
		return &Project{Base: filepath.Join(dir, ".base"), Dir: dir}, nil
	}
	if out, err := GitOut(start, "rev-parse", "--git-common-dir"); err == nil {
		base := strings.TrimSpace(out)
		if base != "" {
			if !filepath.IsAbs(base) {
				base = filepath.Join(start, base)
			}
			if abs, err := filepath.Abs(base); err == nil {
				base = abs
			}
			return &Project{Base: base, Dir: filepath.Dir(base)}, nil
		}
	}
	return nil, fmt.Errorf("not inside a grove project (no .base found). Use 'grove clone URL FOLDER' first")
}

// DefaultBranch returns the bare repo's HEAD branch (the upstream default).
func (p *Project) DefaultBranch() string {
	if out, err := GitOut(p.Base, "symbolic-ref", "--short", "HEAD"); err == nil {
		if b := strings.TrimSpace(out); b != "" {
			return b
		}
	}
	return "main"
}

// Sanitize maps a branch name to a filesystem-/tmux-safe token ('/' and ':' -> '-').
func Sanitize(s string) string {
	return strings.NewReplacer("/", "-", ":", "-").Replace(s)
}

// ---------------------------------------------------------------------------
// Worktree enumeration
// ---------------------------------------------------------------------------

// Worktree is one entry from `git worktree list --porcelain`.
type Worktree struct {
	Path     string
	Branch   string
	Head     string
	Bare     bool
	Detached bool
}

// Worktrees parses the porcelain worktree list for this project.
func (p *Project) Worktrees() ([]Worktree, error) {
	out, err := GitOut(p.Base, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var res []Worktree
	var cur Worktree
	flush := func() {
		if cur.Path != "" {
			res = append(res, cur)
		}
		cur = Worktree{}
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case line == "bare":
			cur.Bare = true
		case line == "detached":
			cur.Detached = true
		case line == "":
			flush()
		}
	}
	flush()
	return res, nil
}

// WorktreePathFor returns the worktree directory for a branch, if one exists.
func (p *Project) WorktreePathFor(branch string) (string, bool) {
	wts, err := p.Worktrees()
	if err != nil {
		return "", false
	}
	for _, w := range wts {
		if w.Branch == branch {
			return w.Path, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Worktree creation
// ---------------------------------------------------------------------------

// EnsureWorktree returns the worktree dir for branch, creating it off the latest
// default branch if needed. copyFiles are untracked files copied from the
// default-branch worktree into freshly created ones. The returned bool reports
// whether the worktree was created on this call (vs. reused), so callers can
// drive one-time bootstrap behavior.
func (p *Project) EnsureWorktree(branch string, copyFiles []string) (string, bool, error) {
	if dir, ok := p.WorktreePathFor(branch); ok {
		ui.Info("Using existing worktree: " + dir)
		if GitQuiet(dir, "pull", "--ff-only") {
			ui.Info("Pulled latest.")
		} else {
			ui.Warn("skipped pull (no upstream or not fast-forward).")
		}
		return dir, false, nil
	}

	ui.Info("Fetching latest...")
	if !GitQuiet(p.Base, "fetch", "origin") {
		ui.Warn("fetch failed; using local refs.")
	}

	def := p.DefaultBranch()
	dir := filepath.Join(p.Dir, Sanitize(branch))
	if pathExists(dir) {
		return "", false, fmt.Errorf("%s already exists but is not a worktree for '%s'", dir, branch)
	}

	switch {
	case GitQuiet(p.Base, "show-ref", "--verify", "--quiet", "refs/heads/"+branch):
		ui.Info(fmt.Sprintf("Creating worktree for existing local branch '%s' at %s", branch, dir))
		if err := Git(p.Base, "worktree", "add", dir, branch); err != nil {
			return "", false, fmt.Errorf("worktree add failed")
		}
	case GitQuiet(p.Base, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch):
		ui.Info(fmt.Sprintf("Creating worktree tracking origin/%s at %s", branch, dir))
		if err := Git(p.Base, "worktree", "add", "--track", "-b", branch, dir, "origin/"+branch); err != nil {
			return "", false, fmt.Errorf("worktree add failed")
		}
	default:
		baseRef := def
		if GitQuiet(p.Base, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+def) {
			baseRef = "origin/" + def
		}
		ui.Info(fmt.Sprintf("Creating new branch '%s' off %s at %s", branch, baseRef, dir))
		// --no-track so autoSetupMerge doesn't point upstream at the base ref; we
		// then track the same-named branch so a plain push/pull targets origin/<branch>.
		if err := Git(p.Base, "worktree", "add", "--no-track", "-b", branch, dir, baseRef); err != nil {
			return "", false, fmt.Errorf("worktree add failed")
		}
		_ = Git(p.Base, "config", "branch."+branch+".remote", "origin")
		_ = Git(p.Base, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	}

	p.setupWorktree(dir, copyFiles)
	return dir, true, nil
}

// Clone creates a new project: a bare .base plus a worktree for the default
// branch. A relative folder is resolved against the current working directory.
func Clone(url, folder string, copyFiles []string) (proj *Project, dir, branch string, err error) {
	if folder == "" {
		folder = strings.TrimSuffix(filepath.Base(url), ".git")
	}
	dest := folder
	if !filepath.IsAbs(dest) {
		wd, err := os.Getwd()
		if err != nil {
			return nil, "", "", err
		}
		dest = filepath.Join(wd, folder)
	}
	if pathExists(dest) {
		return nil, "", "", fmt.Errorf("%s already exists", dest)
	}

	ui.Info(fmt.Sprintf("Cloning %s -> %s/.base", url, dest))
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, "", "", err
	}
	base := filepath.Join(dest, ".base")
	if err := GitPlain("clone", "--bare", url, base); err != nil {
		return nil, "", "", fmt.Errorf("clone failed")
	}
	_ = Git(base, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	if err := Git(base, "fetch", "origin"); err != nil {
		ui.Warn("initial fetch failed.")
	}
	_ = GitQuiet(base, "remote", "set-head", "origin", "-a")

	p := &Project{Base: base, Dir: dest}
	branch = p.DefaultBranch()
	dir = filepath.Join(dest, Sanitize(branch))
	ui.Info(fmt.Sprintf("Creating worktree for '%s' at %s", branch, dir))
	if err := Git(base, "worktree", "add", dir, branch); err != nil {
		return nil, "", "", fmt.Errorf("worktree add failed")
	}
	p.setupWorktree(dir, copyFiles)
	return p, dir, branch, nil
}

// setupWorktree initializes submodules and copies configured untracked files
// from the default-branch worktree. Editor theming is a recipe, not done here.
func (p *Project) setupWorktree(dir string, copyFiles []string) {
	if pathExists(filepath.Join(dir, ".gitmodules")) {
		ui.Info("Initializing submodules...")
		if err := Git(dir, "submodule", "update", "--init", "--recursive"); err != nil {
			ui.Warn("submodule init failed.")
		}
	}
	def := p.DefaultBranch()
	src, ok := p.WorktreePathFor(def)
	if !ok || src == dir {
		return
	}
	for _, f := range copyFiles {
		if f == "" {
			continue
		}
		s := filepath.Join(src, f)
		d := filepath.Join(dir, f)
		if isFile(s) && !pathExists(d) {
			_ = os.MkdirAll(filepath.Dir(d), 0o755)
			if copyFile(s, d) == nil {
				ui.Info("Copied " + f + " from " + def + " worktree.")
			}
		}
	}
}

// RemoveWorktree removes a worktree directory (keeping the branch ref).
func (p *Project) RemoveWorktree(path string, force bool) error {
	if force {
		return Git(p.Base, "worktree", "remove", "--force", path)
	}
	return Git(p.Base, "worktree", "remove", path)
}

// Prune fetches with --prune so gone upstreams are reflected before pruning.
func (p *Project) Prune() {
	if !GitQuiet(p.Base, "fetch", "--prune", "origin") {
		ui.Warn("fetch failed; using local state.")
	}
}

// AddLocalExclude adds a gitignore pattern to the worktree's info/exclude so
// grove's generated artifacts don't show up as dirty in `git status`.
func AddLocalExclude(dir, pattern string) {
	if GitQuiet(dir, "ls-files", "--error-unmatch", strings.TrimPrefix(pattern, "/")) {
		return
	}
	out, err := GitOut(dir, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return
	}
	ex := strings.TrimSpace(out)
	if ex == "" {
		return
	}
	if !filepath.IsAbs(ex) {
		ex = filepath.Join(dir, ex)
	}
	_ = os.MkdirAll(filepath.Dir(ex), 0o755)
	if b, err := os.ReadFile(ex); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if line == pattern {
				return
			}
		}
	}
	f, err := os.OpenFile(ex, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(pattern + "\n")
}

// ---------------------------------------------------------------------------
// small fs helpers
// ---------------------------------------------------------------------------

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
