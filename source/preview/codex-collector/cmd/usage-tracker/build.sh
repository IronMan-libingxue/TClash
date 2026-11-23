#!/bin/sh
set -eu
cd "$(dirname "$0")/../.."
mode=${1:-mac}
out=${TRACKER_DIST:-dist}
if [ "$mode" = release ] && [ -z "${TRACKER_DIST:-}" ]; then
  out=dist/v2
fi
mkdir -p "$out"
out=$(cd "$out" && pwd)
go_bin=${TRACKER_GO:-go}
case "$mode" in
  mac|mac-amd64)
    bundle="$out/Codex 用量.app"
    if [ "${1:-mac}" = mac-amd64 ]; then
      bundle="$out/Codex 用量（Intel）.app"
      export GOOS=darwin GOARCH=amd64 CGO_ENABLED=1
    fi
    mkdir -p "$bundle/Contents/MacOS" "$bundle/Contents/Resources"
    CGO_CFLAGS="-mmacosx-version-min=12.0" CGO_LDFLAGS="-mmacosx-version-min=12.0" \
      "$go_bin" build -mod=readonly -tags production -ldflags="-s -w" \
      -o "$bundle/Contents/MacOS/codex-usage" ./cmd/usage-tracker
    cp cmd/usage-tracker/Info.plist "$bundle/Contents/Info.plist"
    cp LICENSE "$bundle/Contents/Resources/Magpie-LICENSE.txt"
    codesign --force --sign - "$bundle"
    ;;
  windows-amd64|windows-arm64)
    arch=${1#windows-}
    cp LICENSE "$out/Magpie-LICENSE.txt"
    GOOS=windows GOARCH="$arch" CGO_ENABLED=0 "$go_bin" build -mod=readonly \
      -tags production -ldflags="-s -w -H windowsgui" \
      -o "$out/codex-usage-windows-$arch.exe" ./cmd/usage-tracker
    ;;
  release)
    stage=$(mktemp -d "$out/.release.XXXXXX")
    trap 'rm -r "$stage"' EXIT
    export TRACKER_DIST="$stage"
    cmd/usage-tracker/build.sh mac
    cmd/usage-tracker/build.sh mac-amd64
    cmd/usage-tracker/build.sh windows-amd64
    cmd/usage-tracker/build.sh windows-arm64
    bundle="$stage/Codex 用量.app"
    intel="$stage/Codex 用量（Intel）.app/Contents/MacOS/codex-usage"
    binary="$bundle/Contents/MacOS/codex-usage"
    /usr/bin/lipo -create "$binary" "$intel" -output "$binary.universal"
    mv "$binary.universal" "$binary"
    chmod 755 "$binary"
    /usr/bin/codesign --force --sign - "$bundle"
    assets="$stage/installers"
    mkdir -p "$assets"
    /usr/bin/ditto -c -k --keepParent "$bundle" "$assets/Codex Usage Mac.zip"
    cp "$stage"/codex-usage-windows-*.exe "$stage/Magpie-LICENSE.txt" "$assets/"
    if [ -n "${TRACKER_TCLASH_ASSETS:-}" ]; then
      for asset in "TClash Preview Mac.zip" "TClash Preview Windows x64.exe" "TClash Preview Windows arm64.exe" "TClash-LICENSE.txt" "TClash Windows OneClick.exe" "TClash Windows App x64.exe" "TClash Windows App arm64.exe" "TClash Windows Collector x64.exe" "TClash Windows Collector arm64.exe" "TClash Mac OneClick"; do
        test -f "$TRACKER_TCLASH_ASSETS/$asset"
        cp "$TRACKER_TCLASH_ASSETS/$asset" "$assets/$asset"
      done
    fi
    /usr/bin/ditto "$assets" "$bundle/Contents/Resources/installers"
    /usr/bin/codesign --force --sign - "$bundle"
    /usr/bin/codesign --verify --strict "$bundle"
    /usr/bin/ditto "$bundle" "$out/Codex 用量.app"
    /usr/bin/ditto "$stage/Codex 用量（Intel）.app" "$out/Codex 用量（Intel）.app"
    /usr/bin/ditto "$assets" "$out/installers"
    cp "$stage"/codex-usage-windows-*.exe "$stage/Magpie-LICENSE.txt" "$out/"
    /usr/bin/codesign --verify --strict "$out/Codex 用量.app"
    ;;
  *)
    printf '%s\n' "usage: $0 mac|mac-amd64|windows-amd64|windows-arm64|release" >&2
    exit 2
    ;;
esac
