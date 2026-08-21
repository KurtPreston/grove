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
  local -a raw
  mapfile -t raw < <(command grove __complete "${COMP_WORDS[@]:1}" 2>/dev/null) || return 0

  # A __grove_files__ line means "also complete directories here"; it can arrive
  # on its own or mixed in with branch and subcommand candidates.
  local want_dirs=0 reply
  local -a replies=()
  for reply in "${raw[@]}"; do
    if [[ "$reply" == "__grove_files__" ]]; then
      want_dirs=1
    else
      replies+=("$reply")
    fi
  done

  local -a dirs=()
  if (( want_dirs )); then
    mapfile -t dirs < <(compgen -d -S / -- "$cur")
  fi

  COMPREPLY=("${replies[@]}" "${dirs[@]}")
  # Suppress the trailing space only when every candidate is a directory, so
  # the next path segment can be typed straight away.
  if (( ${#replies[@]} == 0 && ${#dirs[@]} > 0 )); then
    compopt -o nospace
  fi
}

complete -F _grove_complete grove
