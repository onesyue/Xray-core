#!/usr/bin/env bash
# Release assets are an explicit job dependency, never an assumed cache hit.
set -euo pipefail

mkdir -p resources
rules_revision=$(git ls-remote https://github.com/Loyalsoldier/v2ray-rules-dat.git refs/heads/release | awk '{print $1}')
[[ "$rules_revision" =~ ^[0-9a-f]{40}$ ]] || { echo 'Invalid rules source revision' >&2; exit 1; }
base="https://raw.githubusercontent.com/Loyalsoldier/v2ray-rules-dat/${rules_revision}"
fetch() {
  curl --fail --location --proto '=https' --proto-redir '=https' \
    --retry 3 --connect-timeout 10 --max-time 120 --output "$2" "$1"
}
for file in geoip.dat geosite.dat; do
  fetch "${base}/${file}.sha256sum" "resources/${file}.sha256sum"
  read -r expected _ < "resources/${file}.sha256sum"
  [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || { echo "Invalid checksum for ${file}" >&2; exit 1; }
  fetch "${base}/${file}" "resources/${file}"
  printf '%s  %s\n' "$expected" "resources/${file}" | sha256sum --check --strict
  rm "resources/${file}.sha256sum"
done
wintun_version=0.14.1
wintun_sha256=07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51
fetch "https://www.wintun.net/builds/wintun-${wintun_version}.zip" resources/wintun.zip
printf '%s  %s\n' "$wintun_sha256" resources/wintun.zip | sha256sum --check --strict
unzip -q -o resources/wintun.zip -d resources
rm resources/wintun.zip
for file in geoip.dat geosite.dat wintun/LICENSE.txt wintun/bin/amd64/wintun.dll wintun/bin/x86/wintun.dll wintun/bin/arm64/wintun.dll; do
  test -s "resources/${file}" || { echo "Missing release asset ${file}" >&2; exit 1; }
done
printf 'rules_revision=%s\nwintun_version=%s\nwintun_sha256=%s\n' \
  "$rules_revision" "$wintun_version" "$wintun_sha256" > resources/ASSET_SOURCE
(cd resources && sha256sum geoip.dat geosite.dat wintun/LICENSE.txt wintun/bin/amd64/wintun.dll wintun/bin/x86/wintun.dll wintun/bin/arm64/wintun.dll ASSET_SOURCE > ASSET_SUMS)
