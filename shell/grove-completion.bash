# Grove tab completion (bash)
# Requires grove with `grove __complete` support (see `grove help`).
#
# Install: source this file from ~/.bashrc after compinit/compgen is available,
# or let dotfiles source it via tabcomplete_grove.

_grove_complete_support() {
  if [[ -n "${_GROVE_COMPLETE_SUPPORT+x}" ]]; then
    return "$_GROVE_COMPLETE_SUPPORT"
  fi
  if command grove help 2>&1 | grep -q __complete; then
    _GROVE_COMPLETE_SUPPORT=0
  else
    _GROVE_COMPLETE_SUPPORT=1
  fi
  return "$_GROVE_COMPLETE_SUPPORT"
}

_grove_complete() {
  if ! _grove_complete_support; then
    return 0
  fi

  local cur="${COMP_WORDS[COMP_CWORD]}"
  local -a replies
  mapfile -t replies < <(command grove __complete "${COMP_WORDS[@]:1}" 2>/dev/null) || return 0

  if (( ${#replies[@]} == 1 )) && [[ "${replies[0]}" == "__grove_files__" ]]; then
    mapfile -t COMPREPLY < <(compgen -f -- "$cur")
    return
  fi

  COMPREPLY=("${replies[@]}")
}

complete -F _grove_complete grove
