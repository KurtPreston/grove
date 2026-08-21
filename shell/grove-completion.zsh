# Grove tab completion (zsh)
# Requires grove with `grove __complete` support (see `grove help`).
#
# Install: source this file from ~/.zshrc after compinit, or let dotfiles source
# it via tabcomplete_grove.

_grove_complete_support() {
  if (( ${+_GROVE_COMPLETE_SUPPORT} )); then
    return "$_GROVE_COMPLETE_SUPPORT"
  fi
  if command grove help 2>&1 | grep -q __complete; then
    _GROVE_COMPLETE_SUPPORT=0
  else
    _GROVE_COMPLETE_SUPPORT=1
  fi
  return "$_GROVE_COMPLETE_SUPPORT"
}

_grove() {
  if ! _grove_complete_support; then
    return
  fi

  local -a raw replies
  raw=("${(@f)$(command grove __complete "${words[@]:1}" 2>/dev/null)}")

  # A __grove_files__ line means "also complete directories here"; it can arrive
  # on its own or mixed in with branch and subcommand candidates.
  local want_dirs=0 reply
  for reply in "${raw[@]}"; do
    if [[ "$reply" == "__grove_files__" ]]; then
      want_dirs=1
    elif [[ -n "$reply" ]]; then
      replies+=("$reply")
    fi
  done

  local ret=1
  (( ${#replies} )) && { compadd -a replies && ret=0 }
  (( want_dirs )) && { _files -/ && ret=0 }
  return ret
}

if (( $+functions[compdef] )); then
  compdef _grove grove
fi
