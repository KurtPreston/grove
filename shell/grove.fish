# grove shell integration (fish)
#
# The grove binary can't change the calling shell's directory, so when a command
# should move you (open/switch/clone/path), grove writes the target worktree path
# to $GROVE_CD_FILE and this wrapper performs the cd. `wt` wraps grove.
#
# Install: source this file from ~/.config/fish/config.fish.

function grove --description "grove worktree workflow launcher"
    set -l gf (mktemp)
    set -lx GROVE_CD_FILE $gf
    command grove $argv
    set -l gs $status
    set -e GROVE_CD_FILE
    if test -s "$gf"
        set -l dest (cat "$gf")
        test -d "$dest"; and cd "$dest"
    end
    rm -f "$gf"
    return $gs
end

function wt --wraps grove --description "alias for grove"
    grove $argv
end
