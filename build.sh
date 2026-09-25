#!/usr/bin/env bash
set -euo pipefail
mkdir -p dist/api dist/challenge
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -tags lambda.norpc -o dist/api/bootstrap ./cmd/api
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -tags lambda.norpc -o dist/challenge/bootstrap ./cmd/challenge
