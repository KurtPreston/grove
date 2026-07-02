#!/usr/bin/env bash
# Download a prebuilt grove release, install the binary, and print optional
# shell-integration instructions. No Go toolchain required. Safe to pipe from curl:
#
#   curl -fsSL https://raw.githubusercontent.com/KurtPreston/grove/main/install.sh | bash
#
# Environment overrides:
#   GROVE_VERSION   release tag to install (e.g. v0.1.0); defaults to the latest release
#   GROVE_REPO      owner/repo to install from (default: KurtPreston/grove)
#   PREFIX          install prefix for the binary (default: $HOME/.local -> $PREFIX/bin)
set -euo pipefail

repo="${GROVE_REPO:-KurtPreston/grove}"
prefix="${PREFIX:-$HOME/.local}"
bindir="$prefix/bin"
sharedir="${XDG_DATA_HOME:-$HOME/.local/share}/grove"

die() { echo "error: $*" >&2; exit 1; }

# Prefer curl, fall back to wget. fetch URL -> stdout.
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1"; }
  fetch_to() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO- "$1"; }
  fetch_to() { wget -qO "$2" "$1"; }
else
  die "need curl or wget to download grove"
fi

# Map uname to the GOOS/GOARCH names GoReleaser uses in archive filenames.
os="$(uname -s)"
case "$os" in
  Linux)  os="linux" ;;
  Darwin) os="darwin" ;;
  *) die "unsupported OS: $os (grove ships linux and darwin builds)" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) die "unsupported architecture: $arch (grove ships amd64 and arm64 builds)" ;;
esac

# Resolve the release tag to install. Capture the response before parsing so the
# curl pipe is never closed early (which would trip SIGPIPE under pipefail).
tag="${GROVE_VERSION:-}"
if [ -z "$tag" ]; then
  resp="$(fetch "https://api.github.com/repos/$repo/releases/latest")" \
    || die "could not query the latest release of $repo (set GROVE_VERSION to pin one)"
  tag="$(printf '%s\n' "$resp" | grep '"tag_name"' | head -n1 | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
  [ -n "$tag" ] || die "could not determine the latest release of $repo (set GROVE_VERSION to pin one)"
fi

archive="grove_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$tag"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading grove $tag ($os/$arch)..."
fetch_to "$base/$archive" "$tmp/$archive" || die "could not download $base/$archive"

# Verify the download against the release checksums (best effort: skip if the
# checksums file or a sha256 tool is unavailable).
if fetch_to "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
  if command -v sha256sum >/dev/null 2>&1; then
    sha256() { sha256sum "$1" | awk '{print $1}'; }
  elif command -v shasum >/dev/null 2>&1; then
    sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
  else
    sha256() { echo ""; }
  fi
  expected="$(grep " ${archive}\$" "$tmp/checksums.txt" | awk '{print $1}')"
  actual="$(sha256 "$tmp/$archive")"
  if [ -n "$expected" ] && [ -n "$actual" ] && [ "$expected" != "$actual" ]; then
    die "checksum mismatch for $archive (expected $expected, got $actual)"
  fi
else
  echo "WARNING: no checksums.txt for $tag; skipping verification." >&2
fi

tar -xzf "$tmp/$archive" -C "$tmp"
[ -f "$tmp/grove" ] || die "archive $archive did not contain a grove binary"

mkdir -p "$bindir"
install -m 0755 "$tmp/grove" "$bindir/grove"
echo "Installed grove -> $bindir/grove"

# Install the shell-integration scripts (bundled in the archive under shell/).
mkdir -p "$sharedir"
for f in grove.bash grove.fish; do
  [ -f "$tmp/shell/$f" ] && install -m 0644 "$tmp/shell/$f" "$sharedir/$f"
done

case ":$PATH:" in
  *":$bindir:"*) ;;
  *) echo "WARNING: $bindir is not on your PATH; add it so 'grove' is found." ;;
esac

cat <<EOF

grove is installed. Run 'grove help' to get started.

Optional: to let the built-in 'cd' recipe move your shell into a worktree, source
grove's shell integration once (skip this unless you add a 'cd' recipe):

  bash/zsh:  echo 'source "$sharedir/grove.bash"' >> ~/.bashrc    # or ~/.zshrc
  fish:      echo 'source "$sharedir/grove.fish"' >> ~/.config/fish/config.fish
EOF
