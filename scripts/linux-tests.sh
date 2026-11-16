#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
test_dir="$PWD/dist/linux-tests"
mkdir -p "$test_dir"
for package in security catalog dao executor controller logic; do
  (cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$test_dir/$package.test" "./internal/$package")
done
for image in "${@:-ubuntu:24.04}"; do
  docker run --rm --network none -v "$test_dir:/tests:ro" "$image" sh -c 'set -e; for t in /tests/*.test; do "$t" -test.v; done'
done
