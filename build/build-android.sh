#!/usr/bin/env bash
# Builds the Android app: binds the Go core (mobile/) together with sing-box's
# libbox into android/app/libs/kordo.aar with sagernet's gomobile fork, then
# builds the APK with Gradle.
#
#   build/build-android.sh [version]
#
# Needs: Go, JDK 17, Android SDK (ANDROID_HOME) with an NDK (ANDROID_NDK_HOME).
# Release signing: KORDO_KEYSTORE (path), KORDO_KEYSTORE_PASSWORD,
# KORDO_KEY_ALIAS, KORDO_KEY_PASSWORD; without them the APK is signed with
# the debug key (fine for testing, but such builds cannot update each other).
# Output: dist/Kordo-<version>.apk + .sha256
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

version="${1:-$(tr -d '[:space:]' < build/version.txt)}"
version="${version#v}"
# libbox needs the same linkname escape hatches as sing-box's own build
tags="$(tr -d '[:space:]' < build/tags.txt),badlinkname,tfogo_checklinkname0"
gomobile_version=v0.1.12 # the one sing-box's go.mod pins

export PATH="$(go env GOPATH)/bin:$PATH"
if ! command -v gomobile >/dev/null || ! command -v gobind >/dev/null; then
  go install "github.com/sagernet/gomobile/cmd/gomobile@$gomobile_version" "github.com/sagernet/gomobile/cmd/gobind@$gomobile_version"
fi
gomobile init

echo "== kordo.aar (Go core + libbox)"
mkdir -p android/app/libs dist
gomobile bind -v \
  -target "${KORDO_ANDROID_TARGETS:-android/arm64,android/arm,android/amd64}" \
  -androidapi 26 \
  -javapkg app.kordo.core \
  -libname kordo \
  -trimpath \
  -ldflags "-s -w -buildid= -checklinkname=0 -X github.com/PeaChack/Kordo/mobile.AppVersion=$version -X github.com/sagernet/sing-box/constant.Version=1.14.4" \
  -tags "$tags" \
  -o android/app/libs/kordo.aar \
  ./mobile github.com/sagernet/sing-box/experimental/libbox

echo "== APK"
(cd android && ./gradlew --no-daemon -PkordoVersion="$version" assembleRelease)

out="dist/Kordo-$version.apk"
cp android/app/build/outputs/apk/release/app-release.apk "$out"
(cd dist && sha256sum "$(basename "$out")" > "$(basename "$out").sha256")
ls -l "$out"
cat "$out.sha256"
