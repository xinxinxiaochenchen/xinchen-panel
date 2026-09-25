#!/usr/bin/env sh
set -eu

mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/control-plane ./cmd/control-plane
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/migrate ./cmd/migrate
