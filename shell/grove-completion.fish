# Grove tab completion (fish)
# Requires grove with `grove __complete` support (see `grove help`).
#
# Install: source this file from ~/.config/fish/config.fish, or let dotfiles
# source it via tabcomplete_grove.

function __grove_complete_support
    if set -q _GROVE_COMPLETE_SUPPORT
        return $_GROVE_COMPLETE_SUPPORT
    end
    if command grove help 2>&1 | grep -q __complete
        set -g _GROVE_COMPLETE_SUPPORT 0
    else
        set -g _GROVE_COMPLETE_SUPPORT 1
    end
    return $_GROVE_COMPLETE_SUPPORT
end

function __grove_complete
    if not __grove_complete_support
        return
    end

    set -l tokens (commandline -opc)
    set -l cur (commandline -ct)
    set -l args
    if test (count $tokens) -gt 1
        set args $tokens[2..-1]
    end
    set -a args $cur

    # A __grove_files__ line means "also complete directories here"; it can
    # arrive on its own or mixed in with branch and subcommand candidates.
    set -l want_dirs 0
    for c in (command grove __complete $args 2>/dev/null)
        if test "$c" = "__grove_files__"
            set want_dirs 1
        else
            echo $c
        end
    end

    if test $want_dirs -eq 1
        __fish_complete_directories $cur
    end
end

complete -c grove -f -a '(__grove_complete)' -d 'grove command'
