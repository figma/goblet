#!/usr/bin/env bash
set -euo pipefail

cd /app
VERSION=$(go list -m -f '{{ .Version }}' github.com/libgit2/git2go/v34)

cd /
git clone https://github.com/libgit2/git2go.git
cd git2go
git checkout "tags/${VERSION}"

echo "Installing git2go at $(git rev-parse --short HEAD) (tag: ${VERSION})"
git submodule update --init # get libgit2
make install-static
