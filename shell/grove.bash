# grove shell integration (bash/zsh)
#
# Optional: this only matters if you use grove's opt-in `cd` recipe. The grove
# binary can't change the calling shell's directory, so the `cd` recipe writes
# the target worktree path to $GROVE_CD_FILE and this wrapper performs the cd
# after grove exits.
#
# Install: source this file from ~/.bashrc or ~/.zshrc.

grove() {
  local _f _dest _status
  _f="$(mktemp)" || return 1
  GROVE_CD_FILE="$_f" command grove "$@"
  _status=$?
  if [ -s "$_f" ]; then
    _dest="$(cat "$_f")"
    [ -d "$_dest" ] && cd "$_dest"
  fi
  rm -f "$_f"
  return $_status
}
