#!/bin/sh
# Install lnch from a GitHub release of lnch-sh/lnch.
# Until a release exists, this script exits and prints the source install.
set -eu

repo="lnch-sh/lnch"
version="${LNCH_VERSION:-}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux) os=linux ;;
  darwin) os=darwin ;;
  mingw*|msys*|cygwin*)
    echo "lnch: on Windows, run install.ps1" >&2
    echo "  powershell -ExecutionPolicy Bypass -File install.ps1" >&2
    echo "Or install from source: go install ./cmd/lnch" >&2
    exit 1
    ;;
  *)
    echo "lnch: install.sh supports linux and macOS (got $os)." >&2
    echo "Install from source: go install ./cmd/lnch" >&2
    exit 1
    ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *)
    echo "lnch: unsupported architecture $arch" >&2
    exit 1
    ;;
esac

if [ -z "$version" ]; then
  version=$(curl -fsSL -H "User-Agent: lnch-install" "https://api.github.com/repos/${repo}/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1 || true)
fi

if [ -z "$version" ]; then
  cat >&2 <<'EOF'
lnch: no GitHub release found for lnch-sh/lnch.
Install from source:

  git clone https://github.com/lnch-sh/lnch
  cd lnch
  go install ./cmd/lnch
EOF
  exit 1
fi

asset="lnch_${os}_${arch}.tar.gz"
url="https://github.com/${repo}/releases/download/${version}/${asset}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if ! curl -fL --retry 3 -o "$tmp/$asset" "$url"; then
  echo "lnch: could not download $url" >&2
  echo "Install from source: go install ./cmd/lnch" >&2
  exit 1
fi

tar -xzf "$tmp/$asset" -C "$tmp"
if [ ! -f "$tmp/lnch" ]; then
  echo "lnch: archive $asset did not contain a lnch binary" >&2
  exit 1
fi

bindir="${LNCH_BIN:-/usr/local/bin}"
if [ ! -d "$bindir" ] || [ ! -w "$bindir" ]; then
  echo "lnch: $bindir is not writable." >&2
  echo "Re-run with sudo, or set LNCH_BIN to a directory on your PATH." >&2
  echo "Example: curl -fsSL https://lnch.sh/install.sh | LNCH_BIN=\"\$HOME/.local/bin\" sh" >&2
  exit 1
fi

install -m 755 "$tmp/lnch" "$bindir/lnch"
echo "lnch installed to $bindir/lnch"
"$bindir/lnch" version
