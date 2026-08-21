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

  local -a replies
  replies=("${(@f)$(command grove __complete "${words[@]:1}" 2>/dev/null)}")
  if (( ${#replies} == 1 )) && [[ "${replies[1]}" == "__grove_files__" ]]; then
    _files
    return
  fi
  compadd -a replies
}

if (( $+functions[compdef] )); then
  compdef _grove grove
fi
