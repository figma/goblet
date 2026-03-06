#!/usr/bin/env bash
set -euo pipefail

cd /app
go mod edit -replace github.com/libgit2/git2go/v34=../git2go
go mod tidy
go build -tags static -o /tmp/goblet-server ./goblet-server
go test -tags static ./...
