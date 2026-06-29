#!/usr/bin/env bash
# Build grove, install the binary, and print shell-integration instructions.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
prefix="${PREFIX:-$HOME/.local}"
bindir="$prefix/bin"

echo "Building grove..."
( cd "$here" && go build -o bin/grove ./cmd/grove )

mkdir -p "$bindir"
install -m 0755 "$here/bin/grove" "$bindir/grove"
echo "Installed grove -> $bindir/grove"

case ":$PATH:" in
  *":$bindir:"*) ;;
  *) echo "WARNING: $bindir is not on your PATH; add it so 'grove' is found." ;;
esac

cat <<EOF

Add shell integration (enables 'cd into worktree' on open/switch):

  bash/zsh:  echo 'source "$here/shell/grove.bash"' >> ~/.bashrc    # or ~/.zshrc
  fish:      echo 'source "$here/shell/grove.fish"' >> ~/.config/fish/config.fish

Then open a new shell and run 'grove help'.
EOF
