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
3. **Hooks** — recipes that gate or react to a branch's lifecycle (validate a
   branch before creating it, bootstrap a dev environment, or any other side
   effect you script yourself).

(1) and (2) are core; (3) is configured in a per-project `grove.json` (see
[Configuration](#configuration)).

## Install

At runtime grove needs `git`; `tmux` enables the `tmux` recipe and `fzf` enables
the interactive picker.

### Prebuilt binary (no Go)

```sh
curl -fsSL https://raw.githubusercontent.com/KurtPreston/grove/main/install.sh | bash
```

This downloads the latest release for your OS/arch and installs the `grove`
binary to `~/.local/bin`. Set `GROVE_VERSION=vX.Y.Z` to pin a version or
`PREFIX=...` to change where the binary lands. You can also download an archive
by hand from the [releases page](https://github.com/KurtPreston/grove/releases).

That's the whole install — `grove` is ready to use.

### Build from source (requires Go)

```sh
git clone <this-repo> grove && cd grove
make install          # builds + installs to ~/.local/bin
```

> Want `grove <branch>` to drop your shell *inside* the worktree it opens? That's
> an opt-in extra that needs a one-line shell hook — see the
> [`cd` recipe](#cd-move-your-shell-into-the-worktree-opt-in). Everything else
> works without it.

### Updating

However you installed it, grove can update itself in place:

```sh
grove update           # fetch the latest release and replace the running binary
grove update --force   # reinstall even if already on the latest version
```

`grove update` resolves the latest release from GitHub, downloads the archive for
your OS/arch, verifies it against the release checksums, and atomically swaps the
running binary. If grove's shell-integration scripts are already installed (under
`$XDG_DATA_HOME/grove`, default `~/.local/share/grove`), they're refreshed too.
Set `GROVE_VERSION=vX.Y.Z` to pin a specific release (e.g. to downgrade) or
`GROVE_REPO=owner/repo` to update from a fork — the same knobs
[`install.sh`](install.sh) honors.

## Usage

| Command | Description |
|---------|-------------|
| `grove clone GIT_URL [FOLDER]` | Clone a repo as a bare `.base` plus a worktree for the default branch under `FOLDER` in the current directory, and seed a starter (commented) `grove.jsonc` |
| `grove BRANCH [--from REF]` | Switch to (or create) BRANCH's worktree and run the hooks in `grove.json`. When BRANCH is new, `beforeCreateBranch` hooks can abort creation, and `--from REF` bases it off REF (see [Choosing the base branch](#choosing-the-base-branch-for-new-branches)) |
| `grove DIR` | When the argument is an existing directory (e.g. `grove .` or `grove ~/Code/slakkr`), run the user-level recipes for it — equivalent to `cd DIR && grove here` (see [Launching any folder](#launching-any-folder)). A directory path takes precedence over the branch interpretation |
| `grove open [BRANCH] [TYPES] [--force]` | Open BRANCH (or the current worktree's branch if omitted/`.`); `TYPES` (comma-separated) filters the configured hooks to those recipe types; `--force` re-runs the `afterFirstOpen` bucket |
| `grove switch [BRANCH]` | Like a bare BRANCH; with no branch and `fzf` installed, opens a picker |
| `grove path BRANCH` | Resolve (creating if needed) BRANCH's worktree and print its absolute path to stdout |
| `grove tmux` | Attach the project's tmux session, building a window for every worktree |
| `grove list` / `ls [--porcelain]` | List worktrees; `--porcelain` prints `branch<TAB>path` to stdout |
| `grove prune [--dry-run]` | Remove worktrees whose branches are merged (including squash/rebase merges) or whose upstream is gone (keeps branch refs); confirming at the prompt discards any local changes in those worktrees. `--dry-run`/`-n` lists candidates without removing anything |
| `grove rm BRANCH [--force]` | Remove a single worktree (keeps the branch ref); `--force` discards local changes |
| `grove color BRANCH` | Print the deterministic color for a branch |
| `grove launch` / `here [DIR]` | Run the user-level recipes for `DIR` (or cwd) without a worktree (see [Launching any folder](#launching-any-folder)) |
| `grove update [--force]` | Update grove in place to the latest published release (downloads, verifies, and swaps the binary); `--force` reinstalls even when already current (see [Updating](#updating)) |
| `grove help` | Show help |

`grove path` and `grove ls --porcelain` write only their result to stdout (all
status/log output goes to stderr), so external tooling can drive grove over SSH.

`grove rm`/`grove prune` remove a worktree even when it contains submodules —
plain `git worktree remove` refuses those, so grove clears git's submodule guard
for you. For `grove rm`, a worktree with local changes (including modified
submodule content) is left in place; pass `grove rm BRANCH --force` to discard
those changes and remove it anyway. `grove prune` lists its candidates (flagging
any with local changes) and, once you confirm at the prompt, discards those
changes and removes them. The branch ref is always kept, so nothing committed is
lost.

### Choosing the base branch for new branches

`grove BRANCH` reuses an existing branch when it finds one — locally, or on
`origin` (fetched and tracked). Only when the branch exists nowhere does grove
create it, and then it has to pick a starting point.

- On the **default branch** (the common case), grove bases the new branch off
  the default branch, exactly as before.
- On a **non-default branch** (e.g. you're on `feature/a` and run
  `grove feature/b`), the base is ambiguous, so grove **asks** which branch to
  branch off — every project branch is offered, with the default and current
  branches surfaced first. With `fzf` installed you get the picker; otherwise a
  numbered menu that also accepts a typed branch name.

Pass `--from REF` to name the base branch up front and skip the prompt (works
with `grove BRANCH`, `grove open`, and `grove path`). This is also what runs in
non-interactive contexts — scripts, pipes, and non-TTY SSH commands never block
on the prompt; without `--from` they fall back to the default branch.

```sh
grove feature/b                 # on a non-default branch: prompts for the base
grove feature/b --from main     # base off main, no prompt
grove feature/b --from feature/a  # stack feature/b on top of feature/a
```

## Hooks

When you open a branch, grove runs the recipes declared in the project's
`grove.json` (see [Configuration](#configuration)), grouped into a `hooks`
object with three buckets that control **when** each recipe runs. Each recipe
entry has a `type` plus that type's settings. A recipe is either **built-in**
or an external executable named `grove-recipe-<type>` found on your `PATH`.

Built-in recipes:

| Recipe | Settings | What it does |
|--------|----------|--------------|
| `tmux` | `layout` | Ensures a per-project tmux session with one window per worktree (colored), one pane per `layout` entry, then attaches/switches |
| `vscode-color-config` | — | Writes the branch color into the worktree's `.vscode/settings.json` (shared by VSCode and Cursor) and keeps it out of `git status` |
| `webhook` | `url`, `token`, `params` | POSTs `params` as JSON to `url` (string values support `$VAR` env substitution) |
| `command` | `command`, `shell` | Runs `command` through a login shell (in the worktree, or the project root for a `beforeCreateBranch` entry — see below) |
| `cd` | — | Moves the calling shell into the worktree. Opt-in; needs grove's shell integration sourced (see [below](#cd-move-your-shell-into-the-worktree-opt-in)) |

### The three hooks buckets

They run in a fixed order — **`beforeCreateBranch`** → **`onOpen`** →
**`afterFirstOpen`** — and only `beforeCreateBranch` can **abort** (by exiting
non-zero); the other two only warn on failure and grove continues.

| Bucket | Runs | On failure |
|--------|------|------------|
| `beforeCreateBranch` | Only for a **brand-new branch** (one that exists neither locally nor on `origin`), before it's created | **Aborts** — nothing is created |
| `onOpen` | On **every open**: creating, reopening, or a plain-folder `grove launch`/`here` | Warns, continues |
| `afterFirstOpen` | Once, **after the first open** of a **freshly created worktree** — this includes checking out an *existing* local/origin branch into a new worktree, not just brand-new branches. Runs after `onOpen`; also re-run by `grove open --force` on an existing worktree | Warns, continues |

`afterFirstOpen` is the old create-only (`"onOpen": false`) behavior; `onOpen`
is the old always-run default. Running `onOpen` **first** means a non-blocking
editor launch (e.g. `cursor $GROVE_DIR`) opens immediately, and the one-time
setup runs afterward rather than gating the window behind a long build.
`beforeCreateBranch` is the place for a script that validates the branch itself
— e.g. checking its name against a ticket — before anything exists that would
need cleaning up if it's wrong. It is **not** run by `grove path` (used by
scripts/tooling) and is skipped when reusing an existing branch, since there's
nothing left to gate.

> **Renamed:** `afterFirstOpen` was previously called `onCreateWorktree` and ran
> *before* `onOpen`. The old key is still accepted as a deprecated alias (grove
> folds it into `afterFirstOpen` with a warning), but note the order flipped:
> update your config and move any terminal-takeover recipe accordingly (see the
> tmux note below).

```json
{
  "hooks": {
    "beforeCreateBranch": [
      { "type": "command", "command": "$GROVE_PROJECT_DIR/grove-hook-jira-check.sh" }
    ],
    "onOpen": [
      { "type": "vscode-color-config" },
      { "type": "command", "command": "cursor $GROVE_DIR" }
    ],
    "afterFirstOpen": [
      { "type": "command", "command": "nvm use && yarn install && yarn build" }
    ]
  }
}
```

### `command`: run a shell command (e.g. per-project setup, or a branch-name gate)

The `command` recipe runs its `command` through a **login shell** (a no-op
when no `command` is set). Put one-time setup in `afterFirstOpen`; because it
runs after `onOpen`, a non-blocking editor launch opens first and the setup
follows:

```json
{
  "hooks": {
    "onOpen": [
      { "type": "vscode-color-config" },
      { "type": "command", "command": "cursor $GROVE_DIR" }
    ],
    "afterFirstOpen": [
      { "type": "command", "command": "nvm use && yarn install && yarn build" }
    ]
  }
}
```

Now `grove some-branch` in that project creates the worktree, opens the editor,
and only then runs the one-time setup command in it.

> **tmux (and other terminal-takeover recipes):** the `tmux` recipe calls
> `attach`, which hands the terminal to tmux and **blocks grove until you
> detach**. Since `onOpen` now runs before `afterFirstOpen`, a `tmux` recipe in
> `onOpen` would defer `afterFirstOpen` until you detach. For a tmux workflow,
> run one-time setup as a pane command in the tmux `layout` (e.g. `"layout":
> "setup=nvm use && yarn install,shell=,claude=claude"`) rather than in
> `afterFirstOpen`.

Notes:

- The command runs through a **login shell** (`bash -l` by default) so your
  shell environment is sourced — that is what makes shell functions like `nvm
  use` work in a non-interactive run. Override the interpreter with the
  recipe's `shell` field (e.g. `"shell": "zsh"`).
- It runs in the **worktree directory**, except for a `beforeCreateBranch`
  entry — the worktree doesn't exist yet there, so it runs in the **project
  root** (`$GROVE_PROJECT_DIR`) instead.
- In `beforeCreateBranch`, a **non-zero exit aborts branch creation** — see
  [Verifying a branch's ticket before creating it](#verifying-a-branchs-ticket-before-creating-it)
  for a worked example.

### `cd`: move your shell into the worktree (opt-in)

By default grove leaves your shell where it is. Add a `cd` recipe to `onOpen`
when you want `grove <branch>` to drop you inside the worktree it just opened:

```json
{ "hooks": { "onOpen": [ { "type": "cd" } ] } }
```

The catch: a binary can't change its parent shell's working directory. So the
`cd` recipe writes the target path to `$GROVE_CD_FILE` and a tiny shell function
does the actual `cd` once grove exits. That function ships with grove, but you
have to source it once from your shell's startup file:

```sh
# bash/zsh
echo 'source "$HOME/.local/share/grove/grove.bash"' >> ~/.bashrc
# fish
echo 'source "$HOME/.local/share/grove/grove.fish"' >> ~/.config/fish/config.fish
```

Building from source? Point `source` at `shell/grove.bash` (or `.fish`) inside
your checkout instead. Without the sourced function the `cd` recipe warns once
and does nothing — every other recipe still runs. Put `cd` before a `tmux`
entry in `onOpen` so the destination is recorded before tmux takes over the
terminal.

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
| `GROVE_BRANCH` | the branch being opened (or about to be created) |
| `GROVE_NAME` | sanitized branch name (`/` and `:` → `-`) |
| `GROVE_DIR` | absolute worktree path — in `beforeCreateBranch` this is the *planned* path; the worktree doesn't exist there yet |
| `GROVE_COLOR` / `GROVE_FG` | branch color and a readable foreground |
| `GROVE_PROJECT` / `GROVE_PROJECT_DIR` | project name and its directory |
| `GROVE_BASE` | path to the bare `.base` repo |
| `GROVE_DEFAULT_BRANCH` | the repo's default branch |
| `GROVE_IN_SSH` | `1` when running inside an SSH session |
| `GROVE_CREATED` | `1` when the worktree was created on this invocation (vs. reopened) |
| `GROVE_RECIPE_*` | the recipe entry's own fields (`GROVE_RECIPE_URL`, `GROVE_RECIPE_TOKEN`, `GROVE_RECIPE_LAYOUT`, `GROVE_RECIPE_COMMAND`, `GROVE_RECIPE_SHELL`, plus string `params` keys) |

For a `beforeCreateBranch` entry, the external recipe's exit code is the gate:
non-zero aborts branch creation, just like the built-in `command` recipe.

### Verifying a branch's ticket before creating it

`beforeCreateBranch` is a good place to catch a mistyped ticket number before
it's baked into a branch name (and a PR title, and a changelog...). A `command`
recipe pointed at a small script can look up the ticket, print its summary, and
ask for confirmation — exiting non-zero aborts creation:

```json
{
  "hooks": {
    "beforeCreateBranch": [
      { "type": "command", "command": "$GROVE_PROJECT_DIR/grove-hook-jira-check.sh" }
    ]
  }
}
```

The script extracts a leading `PROJECT-NNN` from `$GROVE_BRANCH`, fetches it
from your issue tracker, and prompts you to confirm the branch and ticket match
before letting `git worktree add` run. Exit `0` for branches with no ticket
prefix, and whenever stdin isn't a TTY (scripted/non-interactive branch
creation), so the hook never blocks automation — only a human declining the
prompt should abort. This is company/tracker-specific, so it's a
`beforeCreateBranch` `command` recipe you write yourself rather than a grove
built-in; see [Writing your own recipe](#writing-your-own-recipe) if you'd
rather ship it as an external `grove-recipe-<type>` instead.

## Example: remote workflow

With this `grove.json` and a reverse SSH tunnel from your workstation
(`RemoteForward 39788 127.0.0.1:39788`):

```json
{
  "hooks": {
    "onOpen": [
      { "type": "vscode-color-config" },
      { "type": "webhook", "url": "http://127.0.0.1:39788/open", "token": "$GROVE_WEBHOOK_TOKEN", "params": { "host": "devbox", "path": "$GROVE_DIR", "name": "$GROVE_NAME" } }
    ]
  }
}
```

1. You're SSH'd into your dev box. In `~/Code/myproj` you type `grove feature/x`.
2. grove creates (or reuses) the `feature-x` worktree (add a [`cd`
   recipe](#cd-move-your-shell-into-the-worktree-opt-in) if you also want your
   dev-box shell moved into it).
3. `vscode-color-config` writes the branch color into `.vscode/settings.json`.
4. `webhook` POSTs `{host, path, name}` (from `params`) back through the tunnel;
   wsm opens/focuses a remote Cursor window on that path.

## Launching any folder

The same color + open-a-view experience works for **any directory**, not just
grove worktrees. This is handy for ordinary repos (e.g. `~/Code/slakkr`) that you
never cloned with `grove clone`.

Put a **user-level** config at `$XDG_CONFIG_HOME/grove/config.json` (default
`~/.config/grove/config.json`). It uses the same `hooks` shape as `grove.json`,
though only the `onOpen` bucket applies — there's no worktree to create outside
a grove project, so `beforeCreateBranch`/`afterFirstOpen` never run here:

```json
{
  "hooks": {
    "onOpen": [
      { "type": "vscode-color-config" },
      { "type": "webhook", "url": "http://127.0.0.1:39788/open", "token": "$GROVE_WEBHOOK_TOKEN", "params": { "host": "devbox", "path": "$GROVE_DIR", "name": "$GROVE_NAME" } }
    ]
  }
}
```

Then, from inside a non-grove folder:

```sh
grove            # bare grove outside a grove project falls back to launch
grove .          # same
grove ~/Code/slakkr          # any existing directory: launch it in place
grove launch     # explicit; grove here is an alias
grove launch ~/Code/slakkr   # launch a specific directory
```

`grove DIR` works whether or not you're inside a grove project: when the
argument names an existing directory it is launched directly (equivalent to
`cd DIR && grove here`), taking precedence over treating the token as a branch
name. Anything that isn't an existing directory still resolves as a branch.

grove runs your user-level `onOpen` hooks against the directory, using the
**folder name** (`slakkr`) for both the color and the webhook view name. No
worktree is created, and your shell is not moved unless you add a
[`cd` recipe](#cd-move-your-shell-into-the-worktree-opt-in).

Notes:

- **No default hooks are assumed.** With no user config present, the launch is a
  hard error pointing you at the config path — grove never invents behavior.
- The webhook sends whatever you put in `params` (with env substitution) to
  wsm, so the dedicated virtual-desktop view is handled on the workstation.
- Theming a *remote* Cursor window relies on writing the folder's
  `.vscode/settings.json` (added to `.git/info/exclude` locally, just like the
  worktree flow). Drop `vscode-color-config` from the user config if you don't
  want that.
- A bare `grove` inside a grove project behaves as before (fzf picker); the
  no-argument launch fallback only kicks in when no `.base` is found above the
  current directory. An explicit directory argument like `grove .` or
  `grove ~/Code/slakkr`, however, always launches that directory directly.

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
  "hooks": {
    "beforeCreateBranch": [
      { "type": "command", "command": "$GROVE_PROJECT_DIR/grove-hook-jira-check.sh" }
    ],
    "onOpen": [
      { "type": "vscode-color-config" },
      { "type": "webhook", "url": "http://127.0.0.1:39788/open", "token": "$GROVE_WEBHOOK_TOKEN", "params": { "host": "devbox", "path": "$GROVE_DIR", "name": "$GROVE_NAME" } }
    ],
    "afterFirstOpen": [
      { "type": "command", "command": "nvm use && yarn install && yarn build" }
    ]
  }
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `copy` | `[".env"]` | Untracked files copied from the default-branch worktree into new worktrees |
| `hooks` | `{ "onOpen": [{ "type": "tmux" }] }` | The three lifecycle buckets, each an ordered array of recipes (see [Hooks](#hooks)) |
| `prune` | _(see below)_ | Tunes how `grove prune` decides which branches count as merged (see [Prune detection](#prune-detection)) |

When `grove.json` is absent, or its `hooks` key is omitted, grove falls back to
these defaults, so a project works before you write any config. An explicit
`hooks` object (even one that sets only one bucket) is respected exactly, with
the other buckets empty — it does not merge with the defaults. A malformed file
is non-fatal: grove warns and uses the defaults.

### Prune detection

`grove prune` keeps branch refs; candidates with local changes are flagged in
the list, and confirming removal discards those changes. A worktree is a
candidate when its branch is:

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

The only environment input grove reads directly is `GROVE_CD_FILE`, which the
shell integration sets so the opt-in
[`cd` recipe](#cd-move-your-shell-into-the-worktree-opt-in) can tell it where to
move your shell; it is not user configuration.

A separate **user-level** config at `~/.config/grove/config.json` (honoring
`$XDG_CONFIG_HOME`) drives `grove launch` for folders that are not grove
projects. It reuses the `hooks` shape above (only `onOpen` applies) but has
**no defaults** — see [Launching any folder](#launching-any-folder).

## tmux theming

The `tmux` recipe stores the branch color in per-window options `@grove_bg` and
`@grove_fg`. Reference them from your `tmux.conf` window-status format, e.g.:

```tmux
set -g window-status-current-format "#[bg=#{?@grove_bg,#{@grove_bg},cyan},fg=#{?@grove_fg,#{@grove_fg},black},bold] #I:#W "
set -g window-status-format "#[fg=#{?@grove_bg,#{@grove_bg},white}] #I:#W "
```
