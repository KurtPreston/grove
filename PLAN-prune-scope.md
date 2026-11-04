# Plan: prune-scope

## Goal

Make branch cleanup practical in large repositories by allowing prune to fetch
and classify only branches related to local work.

## Scope

Add `grove prune --scope local|all` and `prune.scope`, defaulting to `all`.
Local scope includes local branches and worktrees, origin branches sharing a
local branch's name, and origin branches tracked by local branches. Fetch only
these origin branches plus the default branch, without tags or the global pull
request mirror. Retain normal merge/squash detection, optional forge lookup,
protected branches, dirty-worktree safety, and deletion confirmations.
Other remotes are not deletion targets. Full scope retains existing behavior.

## Execution rules

- All work happens on the `prune-scope` branch. One plan, one branch, one commit per phase.
- Prefix every commit message with `prune-scope: `.
- For each phase: implement only its scope, run validation, fix failures, commit.
- Work around small obstacles and log them under Obstacles. Stop only if a
  fundamental assumption is wrong or a finding reshapes the effort.
- Log existing bugs under Found bugs. Fold in related fixes, save small
  unrelated fixes for separate branches, and leave nuanced ones for discussion.
- After the last phase: update docs and commit, delete this file and commit,
  ask before pushing and opening a public draft pull request, then report.

## Validation

Use real temporary Git repositories to verify bounded fetches, matching names,
renamed upstreams, missing branches, and unrelated branches left untouched.
Verify flag/config precedence and candidate filtering. Run `go test ./...`,
`go vet ./...`, and `go build ./cmd/grove` between phases.

## Phases

### Phase 1: Scoped origin branch selection and fetching

Add project helpers to gather local-related origin names and fetch only those
plus the default branch. Reuse existing single-branch fetch behavior for
branches that were never pushed or were deleted. Test remote updates and
excluded branches/tags/PR refs using real repositories.

### Phase 2: Prune scope integration

Add validated configuration and CLI overrides, scope origin classification,
disable the global pull mirror in local scope, and wire help and completions.
Test defaults, overrides, invalid inputs, and candidate safety. Update README,
schema, and starter config to explain usage and the detection tradeoff.

## Obstacles

None.

## Found bugs

None.
