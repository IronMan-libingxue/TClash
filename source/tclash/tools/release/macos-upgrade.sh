#!/bin/sh
set -eu

if [ "$#" -lt 5 ] || [ "$#" -gt 6 ]; then
  echo 'usage: macos-upgrade.sh APP COLLECTOR OUTPUT APP_SIGN_ID INSTALLER_SIGN_ID [NOTARY_PROFILE]' >&2
  exit 2
fi
app=$1
collector=$2
output=$3
app_sign=$4
package_sign=$5
notary_profile=${6:-}
release_tools=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
if [ -n "$package_sign" ] || [ -n "$notary_profile" ]; then
  case "$app_sign:$package_sign" in
    'Developer ID Application: '*':Developer ID Installer: '*) ;;
    *) echo 'Distribution requires both Developer ID signing identities.' >&2; exit 2 ;;
  esac
  if [ -z "$notary_profile" ]; then
    echo 'Distribution requires a notarytool keychain profile.' >&2
    exit 2
  fi
fi
if [ "$(/usr/libexec/PlistBuddy -c 'Print CFBundleIdentifier' "$app/Contents/Info.plist")" != local.tclash.desktop ]; then
  echo 'Expected a TClash release application.' >&2
  exit 2
fi
app_version=$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$app/Contents/Info.plist")
app_archs=$(/usr/bin/lipo -archs "$app/Contents/MacOS/TClash")
if [ "$(/usr/bin/lipo -archs "$collector")" != "$app_archs" ]; then
  echo 'The TClash collector must match the architecture of the app.' >&2
  exit 2
fi

case "$output" in /*) ;; *) output="$PWD/$output" ;; esac
mkdir -p "$(dirname "$output")"
stage=$(mktemp -d "$(dirname "$output")/.tclash-pkg.XXXXXXXX")
trap '/bin/rm -rf "$stage"' EXIT

sign_code() {
  if [ -n "$notary_profile" ]; then
    set -- --timestamp --options runtime "$@"
  fi
  /usr/bin/codesign --force --sign "$app_sign" "$@"
}

mkdir -p "$stage/payload" "$stage/scripts"
target="$stage/payload/TClash.app"
/usr/bin/ditto "$app" "$target"
/usr/bin/install -m 644 "$release_tools/../../LICENSE" "$target/Contents/Resources/FlClash-LICENSE.txt"
collector_license="$release_tools/../../../TClash-Preview/codex-collector/LICENSE"
if [ -r "$collector_license" ]; then
  /usr/bin/install -m 644 "$collector_license" "$target/Contents/Resources/Magpie-LICENSE.txt"
fi
/usr/bin/install -m 755 "$collector" "$target/Contents/MacOS/tclash-collector"
for code in "$target/Contents/Frameworks/"*.framework "$target/Contents/Frameworks/"*.dylib; do
  [ -e "$code" ] || continue
  sign_code "$code"
done
sign_code "$target/Contents/MacOS/TClashCore"
sign_code "$target/Contents/MacOS/tclash-collector"
sign_code --entitlements "$release_tools/../../macos/Runner/Release.entitlements" "$target"
/usr/bin/codesign --verify --strict --deep "$target"

for arch in arm64 x86_64; do
  /usr/bin/xcrun swiftc -O -target "$arch-apple-macos12.0" \
    "$release_tools/macos-upgrade.swift" -o "$stage/upgrade-$arch"
done
/usr/bin/lipo -create "$stage/upgrade-arm64" "$stage/upgrade-x86_64" \
  -output "$stage/scripts/tclash-upgrade"
sign_code "$stage/scripts/tclash-upgrade"

cat > "$stage/scripts/run-upgrade" <<'SCRIPT'
#!/bin/sh
set -eu
script_dir=$(/usr/bin/dirname "$0")
user=$(/usr/bin/stat -f '%Su' /dev/console)
case "$user" in ''|root|loginwindow|_*) exit 0 ;; esac
uid=$(/usr/bin/id -u "$user")
scratch=$(/usr/bin/mktemp -d /private/tmp/tclash-app-upgrade.XXXXXXXX)
trap '/bin/rm -r "$scratch"' EXIT
/bin/chmod 755 "$scratch"
/usr/bin/install -m 755 "$script_dir/tclash-upgrade" "$scratch/tclash-upgrade"
/bin/launchctl asuser "$uid" /usr/bin/sudo -H -u "$user" "$scratch/tclash-upgrade" "$1"
SCRIPT
cat > "$stage/scripts/preinstall" <<'SCRIPT'
#!/bin/sh
set -eu
[ "${3:-}" = / ] || exit 1
script_dir=$(/usr/bin/dirname "$0")
"$script_dir/run-upgrade" close
SCRIPT
cat > "$stage/scripts/postinstall" <<'SCRIPT'
#!/bin/sh
set -eu
/usr/bin/codesign --verify --deep --strict /Applications/TClash.app
script_dir=$(/usr/bin/dirname "$0")
if ! "$script_dir/run-upgrade" reopen; then
  "$script_dir/run-upgrade" reopen-previous || true
  exit 1
fi
old='/Applications/TClash Preview.app'
if [ -d "$old" ] && [ ! -L "$old" ] &&
   [ "$(/usr/libexec/PlistBuddy -c 'Print CFBundleIdentifier' "$old/Contents/Info.plist" 2>/dev/null || true)" = 'local.tclash.preview' ]; then
  backup=$(/usr/bin/mktemp -d /Applications/.TClash-previous.XXXXXXXX)
  /bin/mv "$old" "$backup/"
fi
SCRIPT
chmod 755 "$stage/scripts/preinstall" "$stage/scripts/postinstall" "$stage/scripts/run-upgrade"

/usr/bin/pkgbuild --analyze --root "$stage/payload" "$stage/components.plist"
python3 - "$stage/components.plist" <<'PY'
from pathlib import Path
import plistlib
import sys

path = Path(sys.argv[1])
entries = plistlib.loads(path.read_bytes())
app = next(entry for entry in entries if entry['RootRelativeBundlePath'] == 'TClash.app')
app['BundleIsRelocatable'] = False
app['BundleHasStrictIdentifier'] = True
path.write_bytes(plistlib.dumps([app]))
PY

set -- --root "$stage/payload" --component-plist "$stage/components.plist" \
  --scripts "$stage/scripts" --identifier local.tclash.desktop.pkg --version "$app_version" \
  --install-location /Applications
if [ -n "$package_sign" ]; then
  set -- "$@" --sign "$package_sign"
fi
installer="$stage/TClash.pkg"
/usr/bin/pkgbuild "$@" "$installer"
if [ -n "$notary_profile" ]; then
  /usr/bin/xcrun notarytool submit "$installer" --keychain-profile "$notary_profile" --wait
  /usr/bin/xcrun stapler staple "$installer"
  /usr/bin/xcrun stapler validate "$installer"
  /usr/sbin/pkgutil --check-signature "$installer"
  /usr/sbin/spctl --assess --type install --verbose "$installer"
fi
/bin/mv -f "$installer" "$output"
