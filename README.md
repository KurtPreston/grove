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

At runtime grove needs `git`; `tmux` enables the `tmux` recipe and `fzf` enables
the interactive picker.

### Prebuilt binary (no Go)

```sh
curl -fsSL https://raw.githubusercontent.com/KurtPreston/grove/main/install.sh | bash
```

This downloads the latest release for your OS/arch, installs the `grove` binary to
`~/.local/bin`, and drops the shell-integration scripts into `~/.local/share/grove/`.
Set `GROVE_VERSION=vX.Y.Z` to pin a version or `PREFIX=...` to change where the
binary lands. You can also download an archive by hand from the
[releases page](https://github.com/KurtPreston/grove/releases).

Then add the shell integration (needed so `grove` can `cd` your shell):

```sh
# bash/zsh
echo 'source "$HOME/.local/share/grove/grove.bash"' >> ~/.bashrc
# fish
echo 'source "$HOME/.local/share/grove/grove.fish"' >> ~/.config/fish/config.fish
```

### Build from source (requires Go)

```sh
git clone <this-repo> grove && cd grove
make install          # builds + installs to ~/.local/bin
```

Source the integration script from your checkout instead:

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
| `grove clone GIT_URL [FOLDER]` | Clone a repo as a bare `.base` plus a worktree for the default branch under `FOLDER` in the current directory, and seed a starter (commented) `grove.jsonc` |
| `grove BRANCH` | Switch to (or create) BRANCH's worktree and run the recipes in `grove.json` |
| `grove open [BRANCH] [TYPES] [--force]` | Open BRANCH (or the current worktree's branch if omitted/`.`); `TYPES` (comma-separated) filters the configured recipes to those types; `--force` re-runs one-time recipes |
| `grove switch [BRANCH]` | Like a bare BRANCH; with no branch and `fzf` installed, opens a picker |
| `grove path BRANCH` | Resolve (creating if needed) BRANCH's worktree and print its absolute path to stdout |
| `grove tmux` | Attach the project's tmux session, building a window for every worktree |
| `grove list` / `ls [--porcelain]` | List worktrees; `--porcelain` prints `branch<TAB>path` to stdout |
| `grove prune [--dry-run]` | Remove worktrees whose branches are merged (including squash/rebase merges) or whose upstream is gone (keeps branch refs); skips any with local changes. `--dry-run`/`-n` lists candidates without removing anything |
| `grove rm BRANCH [--force]` | Remove a single worktree (keeps the branch ref); `--force` discards local changes |
| `grove color BRANCH` | Print the deterministic color for a branch |
| `grove launch` / `here [DIR]` | Run the user-level recipes for `DIR` (or cwd) without a worktree (see [Launching any folder](#launching-any-folder)) |
| `grove help` | Show help |

`grove path` and `grove ls --porcelain` write only their result to stdout (all
status/log output goes to stderr), so external tooling can drive grove over SSH.

`grove rm`/`grove prune` remove a **clean** worktree even when it contains
submodules — plain `git worktree remove` refuses those, so grove clears git's
submodule guard for you. A worktree with local changes (including modified
submodule content) is left in place; pass `grove rm BRANCH --force` to discard
those changes and remove it anyway.

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
| `webhook` | `url`, `token`, `params` | POSTs `params` as JSON to `url` (string values support `$VAR` env substitution) |
| `command` | `command`, `shell` | Runs `command` in the worktree through a login shell. Pair with `"onOpen": false` to run it **once**, only when the worktree is first created |

### When recipes run: `onCreate` / `onOpen`

Every recipe accepts two boolean flags that gate when it runs. Both **default to
`true`**:

- `onCreate` — run when a worktree is **freshly created**.
- `onOpen` — run on **every open**: creating, reopening, or a plain-folder
  `grove launch`/`here`.

Creating a worktree counts as both creating *and* opening it, so a fresh create
runs any recipe that has either flag set. The practical patterns are:

- **Default** (neither flag set): runs every time you open the branch.
- **Create-only** (`"onOpen": false`): runs only when the worktree is first
  created — the old `bootstrap` behavior.

`--force` on `grove open`/`switch` re-runs create-only recipes on a worktree that
already exists.

### `command`: run a shell command (e.g. per-project setup)

The `command` recipe runs its `command` in the worktree through a **login shell**
(a no-op when no `command` is set). For one-time setup on new worktrees, add
`"onOpen": false` and put it *before* `tmux` so it runs before tmux takes over
the terminal:

```json
{
  "recipes": [
    { "type": "command", "command": "nvm use && yarn install && yarn build", "onOpen": false },
    { "type": "vscode-color-config" },
    { "type": "tmux" }
  ]
}
```

Now `grove some-branch` in that project creates the worktree and runs the command
in it once. Notes:

- The command runs in the **worktree directory** through a **login shell**
  (`bash -l` by default) so your shell environment is sourced — that is what
  makes shell functions like `nvm use` work in a non-interactive run. Override
  the interpreter with the recipe's `shell` field (e.g. `"shell": "zsh"`).
- Drop `"onOpen": false` (or set it `true`) to run the command on every open, not
  just on creation.

### `webhook`: generic HTTP POST

The webhook recipe POSTs an arbitrary JSON `params` object to `url`. String
values in `params` (and in `url` / `token`) are expanded with `$VAR` / `${VAR}`
from the grove context environment before the request is sent.

For the remote (SSH) flow, point `url` at the reverse-tunnel endpoint — e.g.
`http://127.0.0.1:39788/open` — which a reverse SSH tunnel
(`RemoteForward 39788 127.0.0.1:39788`) forwards to
[wsm](https://github.com/KurtPreston/wsm) on the machine you SSH'd in from.
When `token` is set, the recipe adds an `Authorization: Bearer <token>` header
(wsm requires a token in all modes).

```json
{
  "type": "webhook",
  "url": "http://127.0.0.1:39788/open",
  "token": "$GROVE_WEBHOOK_TOKEN",
  "params": {
    "host": "devbox",
    "path": "$GROVE_DIR",
    "name": "$GROVE_NAME"
  }
}
```

`GROVE_NAME` is the sanitized branch name (same as grove uses for worktree
directory names). `GROVE_DIR` is the absolute worktree path.

Company-specific companion URLs (JIRA tickets, GitHub PRs, etc.) belong in an
[external recipe](#writing-your-own-recipe) — see
[`docs/grove-recipe-company-links.example.sh`](docs/grove-recipe-company-links.example.sh).

### Writing your own recipe

Use a `type` that isn't built in and drop an executable `grove-recipe-<type>` on
your `PATH`. grove invokes it with the following environment:

| Variable | Meaning |
|----------|---------|
| `GROVE_BRANCH` | the branch being opened |
| `GROVE_NAME` | sanitized branch name (`/` and `:` → `-`) |
| `GROVE_DIR` | absolute worktree path |
| `GROVE_COLOR` / `GROVE_FG` | branch color and a readable foreground |
| `GROVE_PROJECT` / `GROVE_PROJECT_DIR` | project name and its directory |
| `GROVE_BASE` | path to the bare `.base` repo |
| `GROVE_DEFAULT_BRANCH` | the repo's default branch |
| `GROVE_IN_SSH` | `1` when running inside an SSH session |
| `GROVE_CREATED` | `1` when the worktree was created on this invocation (vs. reopened) |
| `GROVE_RECIPE_*` | the recipe entry's own fields (`GROVE_RECIPE_URL`, `GROVE_RECIPE_TOKEN`, `GROVE_RECIPE_LAYOUT`, `GROVE_RECIPE_COMMAND`, `GROVE_RECIPE_SHELL`, plus string `params` keys) |

## Example: remote workflow

With this `grove.json` and a reverse SSH tunnel from your workstation
(`RemoteForward 39788 127.0.0.1:39788`):

```json
{
  "recipes": [
    { "type": "vscode-color-config" },
    { "type": "webhook", "url": "http://127.0.0.1:39788/open", "token": "$GROVE_WEBHOOK_TOKEN", "params": { "host": "devbox", "path": "$GROVE_DIR", "name": "$GROVE_NAME" } }
  ]
}
```

1. You're SSH'd into your dev box. In `~/Code/myproj` you type `grove feature/x`.
2. grove creates (or reuses) the `feature-x` worktree and `cd`s you in.
3. `vscode-color-config` writes the branch color into `.vscode/settings.json`.
4. `webhook` POSTs `{host, path, name}` (from `params`) back through the tunnel;
   wsm opens/focuses a remote Cursor window on that path.

## Launching any folder

The same color + open-a-view experience works for **any directory**, not just
grove worktrees. This is handy for ordinary repos (e.g. `~/Code/slakkr`) that you
never cloned with `grove clone`.

Put a **user-level** config at `$XDG_CONFIG_HOME/grove/config.json` (default
`~/.config/grove/config.json`). It uses the same `recipes` shape as `grove.json`:

```json
{
  "recipes": [
    { "type": "vscode-color-config" },
    { "type": "webhook", "url": "http://127.0.0.1:39788/open", "token": "$GROVE_WEBHOOK_TOKEN", "params": { "host": "devbox", "path": "$GROVE_DIR", "name": "$GROVE_NAME" } }
  ]
}
```

Then, from inside a non-grove folder:

```sh
grove            # bare grove outside a grove project falls back to launch
grove .          # same
grove launch     # explicit; grove here is an alias
grove launch ~/Code/slakkr   # launch a specific directory
```

grove runs your user-level recipes against the directory, using the **folder name**
(`slakkr`) for both the color and the webhook view name. No worktree is created
and your shell is not moved.

Notes:

- **No default recipe is assumed.** With no user config present, the launch is a
  hard error pointing you at the config path — grove never invents behavior.
- The webhook sends whatever you put in `params` (with env substitution) to
  wsm, so the dedicated virtual-desktop view is handled on the workstation.
- Theming a *remote* Cursor window relies on writing the folder's
  `.vscode/settings.json` (added to `.git/info/exclude` locally, just like the
  worktree flow). Drop `vscode-color-config` from the user config if you don't
  want that.
- Inside a grove project, `grove`/`grove .` behave exactly as before; the launch
  fallback only kicks in when no `.base` is found above the current directory.

## Project layout created by `grove clone URL myproj`

```
./myproj/
├── .base/          # bare repo (shared object store) for all worktrees
├── grove.jsonc     # this project's config (machine-local; not committed)
├── main/           # worktree for the default branch
└── feature-x/      # worktree for branch feature/x  ('/' -> '-' in the dir name)
```

## Configuration

All configuration lives in a single `grove.json` at the project root, **beside
`.base`** — not inside a worktree, so it is never committed and can safely hold
machine-specific values (a webhook token, an SSH host alias). grove reads
`grove.jsonc` in preference to `grove.json`, and either extension tolerates `//`
and `/* */` comments plus trailing commas. `grove clone` seeds a starter
`grove.jsonc` (commented, with ready-to-uncomment example recipes); edit it to
taste. See [`examples/`](examples/) for copy-paste configs (e.g. driving wsm
over a reverse SSH tunnel). It is validated by
[`grove.schema.json`](grove.schema.json); add a `$schema` reference for editor
autocomplete and inline validation.

```json
{
  "$schema": "https://raw.githubusercontent.com/KurtPreston/grove/main/grove.schema.json",
  "copy": [".env"],
  "recipes": [
    { "type": "command", "command": "nvm use && yarn install && yarn build", "onOpen": false },
    { "type": "vscode-color-config" },
    { "type": "webhook", "url": "http://127.0.0.1:39788/open", "token": "$GROVE_WEBHOOK_TOKEN", "params": { "host": "devbox", "path": "$GROVE_DIR", "name": "$GROVE_NAME" } },
    { "type": "tmux", "layout": "shell=,claude=claude" }
  ]
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `copy` | `[".env"]` | Untracked files copied from the default-branch worktree into new worktrees |
| `recipes` | `[{ "type": "tmux" }]` | Ordered recipes run on open/switch (see [Recipes](#recipes)) |
| `prune` | _(see below)_ | Tunes how `grove prune` decides which branches count as merged (see [Prune detection](#prune-detection)) |

When `grove.json` is absent grove falls back to these defaults, so a project
works before you write any config. A malformed file is non-fatal: grove warns
and uses the defaults.

### Prune detection

`grove prune` keeps branch refs and only removes clean worktrees. A worktree is
a candidate when its branch is:

- **merged** — an ancestor of `origin/<default>` (`git branch --merged`);
- **squashed** — its net diff already exists in `origin/<default>`, detected via
  patch-equivalence so squash- and rebase-merged branches are caught even though
  their tips are not ancestors (on by default);
- **forge** — matched to a merged pull request reported by the forge (opt-in);
- **gone** — its configured upstream has disappeared after `git fetch --prune`.

The optional `prune` block tunes the squash and forge checks:

```jsonc
"prune": {
  "detectSquash": true,           // default; set false to disable patch-equivalence detection
  "forge": {
    "enabled": false,             // default; when true, consult the forge via the gh CLI
    "repo": "github.com/owner/repo" // optional; overrides the slug derived from origin
  }
}
```

The forge check runs one `gh pr list --state merged` query and treats any branch
matching a merged PR's head as a candidate. It requires `gh` on `PATH` and
authentication for the remote's host; if `gh` is missing, the repo can't be
resolved, or the query fails, grove warns once and falls back to the git-only
checks. Run `grove prune --dry-run` to preview candidates (with their reason)
without removing anything.

The only remaining environment input is `GROVE_CD_FILE`, which the shell wrapper
sets so grove can tell it where to `cd`; it is not user configuration.

A separate **user-level** config at `~/.config/grove/config.json` (honoring
`$XDG_CONFIG_HOME`) drives `grove launch` for folders that are not grove
projects. It reuses the `recipes` shape above but has **no defaults** — see
[Launching any folder](#launching-any-folder).

## tmux theming

The `tmux` recipe stores the branch color in per-window options `@grove_bg` and
`@grove_fg`. Reference them from your `tmux.conf` window-status format, e.g.:

```tmux
set -g window-status-current-format "#[bg=#{?@grove_bg,#{@grove_bg},cyan},fg=#{?@grove_fg,#{@grove_fg},black},bold] #I:#W "
set -g window-status-format "#[fg=#{?@grove_bg,#{@grove_bg},white}] #I:#W "
```
