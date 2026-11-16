#!/usr/bin/env bash
set -euo pipefail
export COPYFILE_DISABLE=1
cd "$(dirname "$0")/.."
release_dir="$PWD/dist/opsview-linux-amd64"
rm -rf "$release_dir"
mkdir -p "$release_dir/bin" "$release_dir/deploy" "$release_dir/packages"
for component in opsview executor plugin-worker pack; do
  binary="opsview-$component"
  [ "$component" != opsview ] || binary=opsview
  (cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$release_dir/bin/$binary" "./cmd/$component")
done
cp deploy/*.service deploy/*.sh "$release_dir/deploy/"
cp -R packages/catalog packages/plugins "$release_dir/packages/"
cp README.md 安装教程.md "$release_dir/"
cp -R docs "$release_dir/"
cp packages/README.md "$release_dir/packages/README.md"
cp -R packages/examples "$release_dir/packages/"
cp install.sh "$release_dir/bin/opsview-install.sh"
cp install.sh dist/install.sh
tar -C dist -czf dist/opsview-linux-amd64.tar.gz opsview-linux-amd64
(cd dist && if command -v sha256sum >/dev/null; then sha256sum opsview-linux-amd64.tar.gz install.sh > SHA256SUMS; else shasum -a 256 opsview-linux-amd64.tar.gz install.sh > SHA256SUMS; fi)
