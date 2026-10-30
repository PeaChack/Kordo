#!/usr/bin/env bash
# Builds the Windows binaries and the NSIS installer on Linux (or macOS).
#
#   build/build-installer.sh [version]      e.g. build/build-installer.sh 0.3.0
#
# Version: the argument (a leading "v" is dropped), otherwise build/version.txt.
# Needs: Go (version from go.mod), makensis >= 3.08 (apt install nsis).
# Output: bin/*.exe, LICENSE, THIRD_PARTY_NOTICES.md and
#         dist/Kordo-Setup-<version>.exe + dist/Kordo-Setup-<version>.exe.sha256
#
# Code signing (optional): set KORDO_SIGN_CMD to a command that signs the file
# given as its last argument in place, e.g.
#   KORDO_SIGN_CMD='osslsigncode-inplace.sh'          (Linux, osslsigncode)
#   KORDO_SIGN_CMD='signtool sign /fd sha256 /tr http://timestamp.digicert.com /td sha256 /a'
# It is applied to every exe in bin/, to the uninstaller and to the installer.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

version="${1:-$(tr -d '[:space:]' < build/version.txt)}"
version="${version#v}"
if ! [[ "$version" =~ ^[0-9]+(\.[0-9]+){0,3}([-+][0-9A-Za-z.+-]+)?$ ]]; then
  echo "bad version: $version" >&2
  exit 1
fi
# Windows resources and NSIS need a purely numeric a.b.c.d version.
IFS=. read -r v1 v2 v3 v4 <<< "${version%%[-+]*}"
winver="${v1:-0}.${v2:-0}.${v3:-0}.${v4:-0}"

tags="$(tr -d '[:space:]' < build/tags.txt)"
winres='github.com/tc-hib/go-winres@v0.3.3'
ld="-s -w -X github.com/PeaChack/Kordo/windows/service.Version=$version"

export GOOS=windows GOARCH=amd64 CGO_ENABLED=0

# Signing hook. Without KORDO_SIGN_CMD the files stay unsigned.
sign() {
  if [[ -n "${KORDO_SIGN_CMD:-}" ]]; then
    echo "== sign $1"
    # shellcheck disable=SC2086 # the command is meant to be word-split
    $KORDO_SIGN_CMD "$1"
  fi
}

echo "== Kordo $version (file version $winver)"

echo "== resources (icon, manifest, version)"
for dir in windows/gui cmd/kordo-svc cmd/kordoctl; do
  (cd "$dir" && GOOS= GOARCH= go run "$winres" make --in winres/winres.json --out rsrc --arch amd64 \
    --product-version "$winver" --file-version "$winver")
done

mkdir -p bin dist
echo "== kordo-svc.exe"
go build -trimpath -tags "$tags" -ldflags "$ld" -o bin/kordo-svc.exe ./cmd/kordo-svc
echo "== kordoctl.exe"
go build -trimpath -ldflags '-s -w' -o bin/kordoctl.exe ./cmd/kordoctl
echo "== Kordo.exe (GUI)"
go build -trimpath -tags 'desktop,production' -ldflags "-s -w -H windowsgui -X main.appVersion=$version" -o bin/Kordo.exe ./windows/gui
cp LICENSE THIRD_PARTY_NOTICES.md bin/

for f in bin/Kordo.exe bin/kordo-svc.exe bin/kordoctl.exe; do sign "$f"; done

echo "== installer"
# Installer icon: build/icon-256.png wrapped into a single-image .ico
# (PNG-compressed icon entries are valid since Windows Vista).
ico="$(mktemp -d)/kordo.ico"
trap 'rm -rf "$(dirname "$ico")"' EXIT
python3 - build/icon-256.png "$ico" <<'PY'
import struct, sys
png = open(sys.argv[1], "rb").read()
# ICONDIR + one ICONDIRENTRY (0 = 256 px), image data right after (offset 22)
hdr = struct.pack("<HHH", 0, 1, 1) + struct.pack("<BBBBHHII", 0, 0, 0, 0, 1, 32, len(png), 22)
open(sys.argv[2], "wb").write(hdr + png)
PY

out="dist/Kordo-Setup-$version.exe"
nsis_args=(-V2 -INPUTCHARSET UTF8
  "-DVERSION=$version" "-DWINVERSION=$winver"
  "-DSRCDIR=$root/bin" "-DOUTFILE=$root/$out" "-DICON=$ico")
if [[ -n "${KORDO_SIGN_CMD:-}" ]]; then
  nsis_args+=("-DSIGN_CMD=$KORDO_SIGN_CMD")
fi
makensis "${nsis_args[@]}" build/installer/kordo.nsi

(cd dist && sha256sum "$(basename "$out")" > "$(basename "$out").sha256")
ls -l bin "$out"
cat "$out.sha256"
