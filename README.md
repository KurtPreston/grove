# grove

A branch-centric worktree and workflow launcher. Command: `grove`.

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

(1) and (2) are core; (3) is configured in a per-project `grove.json` (see
[Configuration](#configuration)).

## Install

Requires Go (to build), plus `git` at runtime. `tmux` enables the `tmux` recipe,
`fzf` enables the interactive picker.

```sh
git clone <this-repo> grove && cd grove
./install.sh          # builds + installs to ~/.local/bin, prints shell setup
```

Then add the shell integration (needed so `grove` can `cd` your shell):

```sh
# bash/zsh
echo 'source "/path/to/grove/shell/grove.bash"' >> ~/.bashrc
# fish
echo 'source "/path/to/grove/shell/grove.fish"' >> ~/.config/fish/config.fish
```

> The binary alone can't change the calling shell's directory, so the shell
> function reads the target path grove writes to `$GROVE_CD_FILE` and performs the
> `cd`.

## Usage

| Command | Description |
|---------|-------------|
| `grove clone GIT_URL [FOLDER]` | Clone a repo as a bare `.base` plus a worktree for the default branch under `FOLDER` in the current directory, and seed a starter `grove.json` |
| `grove BRANCH` | Switch to (or create) BRANCH's worktree and run the recipes in `grove.json` |
| `grove open [BRANCH] [TYPES] [--force]` | Open BRANCH (or the current worktree's branch if omitted/`.`); `TYPES` (comma-separated) filters the configured recipes to those types; `--force` re-runs one-time recipes |
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

When you open a branch, grove runs the recipes declared in the project's
`grove.json` (see [Configuration](#configuration)), in order. Each recipe entry
has a `type` plus that type's settings. A recipe is either **built-in** or an
external executable named `grove-recipe-<type>` found on your `PATH`.

Built-in recipes:

| Recipe | Settings | What it does |
|--------|----------|--------------|
| `tmux` | `layout` | Ensures a per-project tmux session with one window per worktree (colored), one pane per `layout` entry, then attaches/switches |
| `vscode-color-config` | — | Writes the branch color into the worktree's `.vscode/settings.json` (shared by VSCode and Cursor) and keeps it out of `git status` |
| `webhook` | `url`, `token`, `sshHost` | POSTs `{host, path, name}` as JSON to `url` |
| `bootstrap` | `command`, `shell` | Runs `command` (e.g. `nvm use && yarn install && yarn build`) **once**, the first time a worktree is created |

### `bootstrap`: per-project setup on new worktrees

The `bootstrap` recipe runs its `command` the **first time a worktree is
created** (a no-op when you re-open an existing one, and a no-op when no
`command` is set). Put `bootstrap` *before* `tmux` in the recipes array so it
runs before tmux takes over the terminal:

```json
{
  "recipes": [
    { "type": "bootstrap", "command": "nvm use && yarn install && yarn build" },
    { "type": "vscode-color-config" },
    { "type": "tmux" }
  ]
}
```

Now `grove some-branch` in that project creates the worktree and runs the command
in it once. Notes:

- The command runs in the **new worktree directory** through a **login shell**
  (`bash -l` by default) so your shell environment is sourced — that is what
  makes shell functions like `nvm use` work in a non-interactive run. Override
  the interpreter with the recipe's `shell` field (e.g. `"shell": "zsh"`).
- To re-run bootstrap on a worktree that already exists, pass `--force` to
  `grove open`/`switch`.

### `webhook`: open the worktree on another machine

The webhook payload is a loose contract `{host, path, name}` consumed by a
companion workstation listener (e.g. [docent](https://github.com/KurtPreston/docent)),
which opens/focuses a remote editor at `host:path`.

For the remote (SSH) flow, point the recipe's `url` at the reverse-tunnel
endpoint — e.g. `http://127.0.0.1:39787/open` — which a reverse SSH tunnel
(`RemoteForward 39787 127.0.0.1:39787`) forwards to docent on the machine you
SSH'd in from. Set `sshHost` so docent knows which host to open. If `token` is
set, the recipe adds an `Authorization: Bearer <token>` header so the listener
can require a shared secret (docent does, when its own token is configured).

```json
{ "type": "webhook", "url": "http://127.0.0.1:39787/open", "token": "secret", "sshHost": "devbox" }
```

### Writing your own recipe

Use a `type` that isn't built in and drop an executable `grove-recipe-<type>` on
your `PATH`. grove invokes it with the following environment:

| Variable | Meaning |
|----------|---------|
| `GROVE_BRANCH` | the branch being opened |
| `GROVE_DIR` | absolute worktree path |
| `GROVE_COLOR` / `GROVE_FG` | branch color and a readable foreground |
| `GROVE_PROJECT` / `GROVE_PROJECT_DIR` | project name and its directory |
| `GROVE_BASE` | path to the bare `.base` repo |
| `GROVE_DEFAULT_BRANCH` | the repo's default branch |
| `GROVE_IN_SSH` | `1` when running inside an SSH session |
| `GROVE_CREATED` | `1` when the worktree was created on this invocation (vs. reopened) |
| `GROVE_RECIPE_*` | the recipe entry's own fields (`GROVE_RECIPE_URL`, `GROVE_RECIPE_TOKEN`, `GROVE_RECIPE_SSH_HOST`, `GROVE_RECIPE_LAYOUT`, `GROVE_RECIPE_COMMAND`, `GROVE_RECIPE_SHELL`) |

## Example: remote workflow

With this `grove.json` and a reverse SSH tunnel from your workstation
(`RemoteForward 39787 127.0.0.1:39787`):

```json
{
  "recipes": [
    { "type": "vscode-color-config" },
    { "type": "webhook", "url": "http://127.0.0.1:39787/open", "sshHost": "devbox" }
  ]
}
```

1. You're SSH'd into your dev box. In `~/Code/myproj` you type `grove feature/x`.
2. grove creates (or reuses) the `feature-x` worktree and `cd`s you in.
3. `vscode-color-config` writes the branch color into `.vscode/settings.json`.
4. `webhook` POSTs `{host, path, name}` back through the tunnel; your
   workstation listener opens/focuses a remote Cursor window on that path.

## Project layout created by `grove clone URL myproj`

```
./myproj/
├── .base/          # bare repo (shared object store) for all worktrees
├── grove.json      # this project's config (machine-local; not committed)
├── main/           # worktree for the default branch
└── feature-x/      # worktree for branch feature/x  ('/' -> '-' in the dir name)
```

## Configuration

All configuration lives in a single `grove.json` at the project root, **beside
`.base`** — not inside a worktree, so it is never committed and can safely hold
machine-specific values (a webhook token, an SSH host alias). `grove clone`
seeds a starter file; edit it to taste. It is validated by
[`grove.schema.json`](grove.schema.json); add a `$schema` reference for editor
autocomplete and inline validation.

```json
{
  "$schema": "https://raw.githubusercontent.com/KurtPreston/grove/main/grove.schema.json",
  "copy": [".env"],
  "recipes": [
    { "type": "bootstrap", "command": "nvm use && yarn install && yarn build" },
    { "type": "vscode-color-config" },
    { "type": "webhook", "url": "http://127.0.0.1:39787/open", "token": "secret", "sshHost": "devbox" },
    { "type": "tmux", "layout": "shell=,claude=claude" }
  ]
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `copy` | `[".env"]` | Untracked files copied from the default-branch worktree into new worktrees |
| `recipes` | `[{ "type": "tmux" }]` | Ordered recipes run on open/switch (see [Recipes](#recipes)) |

When `grove.json` is absent grove falls back to these defaults, so a project
works before you write any config. A malformed file is non-fatal: grove warns
and uses the defaults.

The only remaining environment input is `GROVE_CD_FILE`, which the shell wrapper
sets so grove can tell it where to `cd`; it is not user configuration.

## tmux theming

The `tmux` recipe stores the branch color in per-window options `@grove_bg` and
`@grove_fg`. Reference them from your `tmux.conf` window-status format, e.g.:

```tmux
set -g window-status-current-format "#[bg=#{?@grove_bg,#{@grove_bg},cyan},fg=#{?@grove_fg,#{@grove_fg},black},bold] #I:#W "
set -g window-status-format "#[fg=#{?@grove_bg,#{@grove_bg},white}] #I:#W "
```
