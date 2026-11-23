#!/bin/sh
set -eu
umask 077

if [ "$#" -ne 2 ]; then
  echo 'Usage: build-package.sh <device-installer.app> <output.pkg>' >&2
  exit 2
fi

source_app=$1
output=$2
tools_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)
resource_dir="$source_app/Contents/Resources"
test -f "$resource_dir/enrollment.json"
test -f "$resource_dir/TClash Preview Mac.zip"
test ! -e "$output"
device_name=$(/usr/bin/plutil -extract name raw -o - "$resource_dir/enrollment.json")
case "$device_name" in
  'Mac 2'|'Mac 3'|'Mac 4'|'Mac 5'|'Mac 6') ;;
  *) echo 'Unsupported Mac device identity.' >&2; exit 1 ;;
esac
package_id="local.tclash.preview.mac${device_name#Mac }"

work=$(/usr/bin/mktemp -d /private/tmp/tclash-pkg-build.XXXXXXXX)
trap '/bin/rm -rf "$work"' EXIT HUP INT TERM
mkdir -p "$work/payload/Applications" "$work/scripts" "$work/cache" "$work/resources"
export CLANG_MODULE_CACHE_PATH="$work/cache/clang"
export SWIFT_MODULECACHE_PATH="$work/cache/swift"

for arch in arm64 x86_64; do
  /usr/bin/swiftc -module-cache-path "$work/cache/swift" -target "$arch-apple-macos11.0" -O \
    "$tools_dir/enrollment.swift" -o "$work/helper-$arch"
done
/usr/bin/lipo -create "$work/helper-arm64" "$work/helper-x86_64" -output "$work/scripts/tclash-pkg-helper"
"$work/scripts/tclash-pkg-helper" --self-test

/usr/bin/ditto -x -k "$resource_dir/TClash Preview Mac.zip" "$work/payload/Applications"
target="$work/payload/Applications/TClash Preview.app"
/usr/bin/codesign --verify --strict --deep "$target"
[ "$("$target/Contents/MacOS/tclash-collector" -build-info)" = tclash-collector-device-reset-v1 ]
[ "$(/usr/bin/plutil -extract CFBundleIdentifier raw -o - "$target/Contents/Info.plist")" = local.tclash.preview ]

/bin/cp "$resource_dir/enrollment.json" "$work/scripts/enrollment.json"
/bin/cp "$tools_dir/package-script" "$work/scripts/preinstall"
/bin/cp "$tools_dir/package-script" "$work/scripts/postinstall"
/bin/chmod 600 "$work/scripts/enrollment.json"
/bin/chmod 700 "$work/scripts/preinstall" "$work/scripts/postinstall" "$work/scripts/tclash-pkg-helper"
/bin/sh -n "$work/scripts/preinstall"
/bin/sh -n "$work/scripts/postinstall"

/usr/bin/pkgbuild --analyze --root "$work/payload" "$work/component.plist" >/dev/null
/usr/libexec/PlistBuddy -c 'Set :0:BundleIsRelocatable false' "$work/component.plist"
/usr/libexec/PlistBuddy -c 'Set :0:BundleIsVersionChecked false' "$work/component.plist"
/usr/libexec/PlistBuddy -c 'Set :0:BundleHasStrictIdentifier true' "$work/component.plist"
/usr/libexec/PlistBuddy -c 'Set :0:BundleOverwriteAction upgrade' "$work/component.plist"
/usr/bin/pkgbuild --root "$work/payload" --component-plist "$work/component.plist" \
  --scripts "$work/scripts" --identifier "$package_id" --version 1.0.0 \
  --install-location / "$work/component.pkg"

cat > "$work/distribution.xml" <<XML
<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="1">
  <title>TClash Preview · $device_name</title>
  <options customize="never" require-scripts="true" hostArchitectures="arm64,x86_64"/>
  <domains enable_localSystem="true" enable_currentUserHome="false" enable_anywhere="false"/>
  <welcome file="Welcome.txt" mime-type="text/plain"/>
  <choices-outline><line choice="default"><line choice="tclash"/></line></choices-outline>
  <choice id="default"/>
  <choice id="tclash" visible="false"><pkg-ref id="$package_id"/></choice>
  <pkg-ref id="$package_id" version="1.0.0" onConclusion="none">component.pkg</pkg-ref>
</installer-gui-script>
XML
cat > "$work/resources/Welcome.txt" <<TEXT
TClash Preview · $device_name

此安装程序会将 TClash Preview 安装到“应用程序”，并自动加载 $device_name 的设备授权。
安装完成后请保持联网，并在 A 端查看设备是否接入。

此安装包包含 $device_name 的私人授权，请仅私下传送至该设备。
TEXT
/usr/bin/productbuild --distribution "$work/distribution.xml" --resources "$work/resources" \
  --package-path "$work" "$work/final.pkg"
/bin/chmod 600 "$work/final.pkg"
/bin/mv "$work/final.pkg" "$output"
