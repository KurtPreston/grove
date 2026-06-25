# grove

A branch-centric worktree and workflow launcher. Command: `grove` (alias: `wt`).

`grove` manages multiple concurrent branches as [git worktrees](https://git-scm.com/docs/git-worktree)
instead of juggling several full clones: one bare "base" repo per project, one
worktree per branch in a predictable folder, and a deterministic per-branch color.
On top of that it runs **recipes** — pluggable actions that bootstrap your dev
environment for a branch (a tmux session, editor theming, a webhook to another
machine, or anything you script yourself).

Responsibilities are deliberately separated:

1. **Worktrees** — one named folder per branch (git is the source of truth; there
   is no separate state file to drift).
2. **Metadata** — a stable color assigned to each branch.
3. **Recipes** — trigger a development environment / side effect for the branch.

(1) and (2) are core; (3) is configurable via `GROVE_RECIPES`.

## Install

Requires Go (to build), plus `git` at runtime. `tmux` enables the `tmux` recipe,
`fzf` enables the interactive picker.

```sh
git clone <this-repo> grove && cd grove
./install.sh          # builds + installs to ~/.local/bin, prints shell setup
```

Then add the shell integration (needed so `grove`/`wt` can `cd` your shell):

```sh
# bash/zsh
echo 'source "/path/to/grove/shell/grove.bash"' >> ~/.bashrc
# fish
echo 'source "/path/to/grove/shell/grove.fish"' >> ~/.config/fish/config.fish
```

> The binary alone can't change the calling shell's directory, so the shell
> function reads the target path grove writes to `$GROVE_CD_FILE` and performs the
> `cd`. The `wt` alias resolves to the same function.

## Usage

| Command | Description |
|---------|-------------|
| `grove clone GIT_URL [FOLDER]` | Clone a repo as a bare `.base` plus a worktree for the default branch under `$CODE_HOME/FOLDER` |
| `grove BRANCH` | Switch to (or create) BRANCH's worktree and run `GROVE_RECIPES` |
| `grove open [BRANCH] [RECIPES]` | Open BRANCH (or the current worktree's branch if omitted/`.`) and run RECIPES (defaults to `GROVE_RECIPES`) |
| `grove switch [BRANCH]` | Like a bare BRANCH; with no branch and `fzf` installed, opens a picker |
| `grove path BRANCH` | Resolve (creating if needed) BRANCH's worktree and print its absolute path to stdout |
| `grove tmux` | Attach the project's tmux session, building a window for every worktree |
| `grove list` / `ls [--porcelain]` | List worktrees; `--porcelain` prints `branch<TAB>path` to stdout |
| `grove prune` | Remove worktrees whose branches are merged or whose upstream is gone (keeps branch refs) |
| `grove rm BRANCH [--force]` | Remove a single worktree (keeps the branch ref) |
| `grove color BRANCH` | Print the deterministic color for a branch |
| `grove help` | Show help |

`grove path` and `grove ls --porcelain` write only their result to stdout (all
status/log output goes to stderr), so external tooling can drive grove over SSH.

## Recipes

When you open a branch, grove runs the recipes in `GROVE_RECIPES` (or the ones you
pass as the second argument to `grove open`). A recipe is either **built-in** or an
external executable named `grove-recipe-<name>` found on your `PATH`.

Built-in recipes:

| Recipe | What it does |
|--------|--------------|
| `tmux` | Ensures a per-project tmux session with one window per worktree (colored), one pane per `GROVE_TMUX_LAYOUT` entry, then attaches/switches |
| `vscode-color-config` | Writes the branch color into the worktree's `.vscode/settings.json` (shared by VSCode and Cursor) and keeps it out of `git status` |
| `webhook` | POSTs `{host, path, name}` as JSON to `GROVE_WEBHOOK_URL` |
| `ssh-source-webhook` | When inside an SSH session, POSTs `{host, path, name}` to `http://127.0.0.1:$GROVE_WEBHOOK_PORT/open` — intended to reach a listener on the machine you SSH'd in from via a reverse SSH tunnel |

The webhook payload is a loose contract `{host, path, name}` consumed by a
companion workstation listener (e.g. [docent](https://github.com/KurtPreston/docent)),
which opens/focuses a remote editor at `host:path`.

### Writing your own recipe

Drop an executable `grove-recipe-foo` on your `PATH`. grove invokes it with the
following environment:

| Variable | Meaning |
|----------|---------|
| `GROVE_BRANCH` | the branch being opened |
| `GROVE_DIR` | absolute worktree path |
| `GROVE_COLOR` / `GROVE_FG` | branch color and a readable foreground |
| `GROVE_PROJECT` / `GROVE_PROJECT_DIR` | project name and its directory |
| `GROVE_BASE` | path to the bare `.base` repo |
| `GROVE_DEFAULT_BRANCH` | the repo's default branch |
| `GROVE_SSH_HOST` | configured Remote-SSH host alias (for webhooks) |
| `GROVE_IN_SSH` | `1` when running inside an SSH session |

## Example: remote workflow

With `GROVE_RECIPES="ssh-source-webhook,vscode-color-config"` and a reverse SSH
tunnel from your workstation (`RemoteForward 39787 127.0.0.1:39787`):

1. You're SSH'd into your dev box. In `~/Code/myproj` you type `wt feature/x`.
2. grove creates (or reuses) the `feature-x` worktree and `cd`s you in.
3. `vscode-color-config` writes the branch color into `.vscode/settings.json`.
4. `ssh-source-webhook` POSTs `{host, path, name}` back through the tunnel; your
   workstation listener opens/focuses a remote Cursor window on that path.

## Project layout created by `grove clone URL myproj`

```
$CODE_HOME/myproj/
├── .base/          # bare repo (shared object store) for all worktrees
├── main/           # worktree for the default branch
└── feature-x/      # worktree for branch feature/x  ('/' -> '-' in the dir name)
```

## Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `CODE_HOME` | `~/Code` | Base directory for new projects |
| `GROVE_RECIPES` | `tmux` | Comma-separated recipes run by open/switch |
| `GROVE_TMUX_LAYOUT` | `shell=,claude=claude` | tmux panes as `name=cmd` pairs, left-to-right (empty cmd = plain shell) |
| `GROVE_COPY` | `.env` | Colon-separated untracked files copied into new worktrees |
| `GROVE_PALETTE` | built-in | Override the branch color palette (space/comma-separated hex) |
| `GROVE_WEBHOOK_URL` | — | Target URL for the `webhook` recipe |
| `GROVE_WEBHOOK_PORT` | `39787` | Port for `ssh-source-webhook` |
| `GROVE_SSH_HOST` | — | Remote-SSH host alias embedded in webhook payloads |

## tmux theming

The `tmux` recipe stores the branch color in per-window options `@grove_bg` and
`@grove_fg`. Reference them from your `tmux.conf` window-status format, e.g.:

```tmux
set -g window-status-current-format "#[bg=#{?@grove_bg,#{@grove_bg},cyan},fg=#{?@grove_fg,#{@grove_fg},black},bold] #I:#W "
set -g window-status-format "#[fg=#{?@grove_bg,#{@grove_bg},white}] #I:#W "
```
