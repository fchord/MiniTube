#!/usr/bin/env bash
# Bundle Mediabunny + shorts-worker-src.js → shorts-worker.js (no npm required).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/api/internal/httpapi/shorts-worker-src.js"
OUT="$ROOT/api/internal/httpapi/shorts-worker.js"
MB_VER="${MEDIANBUNNY_VERSION:-1.56.1}"
ESBUILD_VER="${ESBUILD_VERSION:-0.25.3}"
DIR="$(mktemp -d)"
trap 'rm -rf "$DIR"' EXIT
cd "$DIR"
curl -fsSL "https://registry.npmjs.org/mediabunny/-/mediabunny-${MB_VER}.tgz" -o mb.tgz
mkdir mb && tar -xzf mb.tgz -C mb
curl -fsSL "https://registry.npmjs.org/@esbuild/linux-x64/-/linux-x64-${ESBUILD_VER}.tgz" -o esb.tgz
mkdir esb && tar -xzf esb.tgz -C esb
chmod +x esb/package/bin/esbuild
mkdir -p node_modules
ln -s "$DIR/mb/package" node_modules/mediabunny
cp "$SRC" worker.js
esb/package/bin/esbuild worker.js --bundle --format=esm --outfile="$OUT"
echo "wrote $OUT"
