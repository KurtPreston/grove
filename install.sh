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

# Prefer curl, fall back to wget. fetch URL -> stdout, fetch_to URL FILE, and
# resolve URL -> the URL a redirect points at.
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1"; }
  fetch_to() { curl -fsSL "$1" -o "$2"; }
  resolve() { curl -fsSLI -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO- "$1"; }
  fetch_to() { wget -qO "$2" "$1"; }
  # wget exits non-zero on the redirect it was told not to follow, so ignore the
  # status and judge by whether a Location came back.
  resolve() {
    wget -q --max-redirect=0 -S -O /dev/null "$1" 2>&1 |
      awk 'tolower($1) == "location:" { print $2; exit }'
  }
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

# Resolve the release tag to install from the redirect /releases/latest serves to
# /releases/tag/<tag>. Deliberately not api.github.com: unauthenticated API calls
# are capped at 60 per hour per source IP, so behind a shared NAT the budget is
# spent by unrelated traffic and this 403s for everyone on it. A repo with no
# releases redirects to /releases instead, leaving no tag to match.
tag="${GROVE_VERSION:-}"
if [ -z "$tag" ]; then
  url="$(resolve "https://github.com/$repo/releases/latest" || true)"
  tag="$(printf '%s\n' "$url" | sed -n 's#.*/releases/tag/\([^/?]*\).*#\1#p')"
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
for f in grove.bash grove.fish grove-completion.bash grove-completion.zsh grove-completion.fish; do
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

Optional: for tab completion of branch names and subcommands, source the
completion script for your shell (after compinit in zsh):

  bash:      echo 'source "$sharedir/grove-completion.bash"' >> ~/.bashrc
  zsh:       echo 'source "$sharedir/grove-completion.zsh"' >> ~/.zshrc
  fish:      echo 'source "$sharedir/grove-completion.fish"' >> ~/.config/fish/config.fish
EOF
