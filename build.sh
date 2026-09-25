#!/usr/bin/env bash
set -euo pipefail
for name in signup resend confirm challenge; do
  mkdir -p "dist/$name"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -tags lambda.norpc -o "dist/$name/bootstrap" "./cmd/$name"
  chmod 755 "dist/$name/bootstrap"
  rm -f "dist/$name.zip"
  (cd "dist/$name" && zip -q -X "../$name.zip" bootstrap)
done
