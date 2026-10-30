#!/usr/bin/env bash
# Signs a Windows executable in place with osslsigncode (Linux/macOS).
# Used as KORDO_SIGN_CMD by build/build-installer.sh:
#
#   KORDO_PFX=/path/cert.pfx KORDO_PFX_PASSWORD=... \
#   KORDO_SIGN_CMD="$PWD/build/sign-osslsigncode.sh" build/build-installer.sh 0.3.0
#
# KORDO_TIMESTAMP_URL overrides the timestamp server; "none" skips the
# timestamp (local tests only: the signature then expires with the
# certificate).
set -euo pipefail
file="$1"
: "${KORDO_PFX:?set KORDO_PFX to the .pfx certificate}"
ts="${KORDO_TIMESTAMP_URL:-http://timestamp.digicert.com}"
tsargs=(-ts "$ts")
[ "$ts" = none ] && tsargs=()
tmp="$file.signed"
osslsigncode sign -pkcs12 "$KORDO_PFX" -pass "${KORDO_PFX_PASSWORD:-}" \
  -h sha256 -n "Kordo" -i "https://github.com/PeaChack/Kordo" "${tsargs[@]}" \
  -in "$file" -out "$tmp"
mv -f "$tmp" "$file"
echo "signed: $file"
