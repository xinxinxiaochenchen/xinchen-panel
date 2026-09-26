#!/usr/bin/env sh
set -eu

mkdir -p bin
npm --prefix apps/web ci --no-audit --no-fund
npm --prefix apps/web run build
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${GO_BIN:-go}" build -trimpath -o bin/control-plane ./cmd/control-plane
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${GO_BIN:-go}" build -trimpath -o bin/migrate ./cmd/migrate
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${GO_BIN:-go}" build -trimpath -o bin/admin-bootstrap ./cmd/admin-bootstrap
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${GO_BIN:-go}" build -trimpath -o bin/agent ./cmd/agent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${GO_BIN:-go}" build -trimpath -o bin/agent-token ./cmd/agent-token
