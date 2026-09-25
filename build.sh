#!/usr/bin/env bash
set -euo pipefail
mkdir -p dist/api dist/challenge
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -tags lambda.norpc -o dist/api/bootstrap ./cmd/api
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -tags lambda.norpc -o dist/challenge/bootstrap ./cmd/challenge
chmod 755 dist/api/bootstrap dist/challenge/bootstrap
rm -f dist/api.zip dist/challenge.zip
(cd dist/api && zip -q -X ../api.zip bootstrap)
(cd dist/challenge && zip -q -X ../challenge.zip bootstrap)
