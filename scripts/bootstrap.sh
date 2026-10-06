#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TOOLS="$ROOT/.cache/toolchain"
GO_VERSION="1.23.4"
TAILWIND_VERSION="v3.4.17"

mkdir -p "$TOOLS"

if [ ! -x "$TOOLS/go/bin/go" ]; then
  echo "==> downloading Go $GO_VERSION"
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz
  tar -C "$TOOLS" -xzf /tmp/go.tgz
  rm -f /tmp/go.tgz
fi

if [ ! -x "$TOOLS/tailwindcss" ]; then
  echo "==> downloading Tailwind CLI $TAILWIND_VERSION"
  curl -fsSL -o "$TOOLS/tailwindcss" \
    "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-x64"
  chmod +x "$TOOLS/tailwindcss"
fi

export PATH="$TOOLS/go/bin:$ROOT/.cache/gopath/bin:$PATH"
export GOPATH="$ROOT/.cache/gopath"

echo "==> go mod download"
(cd "$ROOT" && go mod download)

if ! command -v staticcheck >/dev/null 2>&1; then
  echo "==> installing staticcheck"
  go install honnef.co/go/tools/cmd/staticcheck@2024.1.1
fi

echo
echo "Toolchain ready. Add this to your shell:"
echo "  export PATH=$TOOLS/go/bin:$ROOT/.cache/gopath/bin:\$PATH"
echo "  export GOPATH=$ROOT/.cache/gopath"
echo
echo "Then: make css && make run"

"$GOROOT/bin/go" telemetry off >/dev/null 2>&1 || true
