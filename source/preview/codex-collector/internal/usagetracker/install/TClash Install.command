#!/bin/zsh
set -eu
umask 077
cd -- "${0:A:h}"
target="$HOME/Applications/TClash Preview.app"
data="$HOME/Library/Application Support/TClash Preview/collector"
legacy="$HOME/Library/Application Support/Codex Usage Tracker/tracker.json"
if pgrep -f "$target/Contents/MacOS/clash-verge" >/dev/null; then
  printf '%s\n' '请先退出 TClash Preview，再重新打开此安装程序。'
  exit 1
fi
if [[ -f "$legacy" ]]; then
  role=$(/usr/bin/plutil -extract role raw -o - "$legacy" 2>/dev/null || true)
  if [[ "$role" == a ]]; then
    printf '%s\n' '本机已有 A 端，请勿将此远端 B 安装包安装在 A 端设备。'
  else
    printf '%s\n' '请先退出旧 B 端，并按说明清理旧 B 端数据后重试。'
  fi
  exit 1
fi
if [[ -f "$data/tracker.json" || -f "$data/use-legacy-tracker" ]]; then
  printf '%s\n' '发现旧 B 端数据，请按说明清理旧 B 端数据后重试。'
  exit 1
fi
[[ -f enrollment.json ]] || { printf '%s\n' '安装授权文件缺失。'; exit 1; }
mkdir -p -- "$HOME/Applications"
stage=$(mktemp -d -t tclash-install)
cleanup() {
  if [[ -d "$stage/previous.app" && ! -d "$target" ]]; then
    /bin/mv -- "$stage/previous.app" "$target" || true
  fi
  /bin/rm -rf -- "$stage"
}
trap cleanup EXIT
/usr/bin/ditto -x -k 'TClash Preview Mac.zip' "$stage"
new="$stage/TClash Preview.app"
/usr/bin/codesign --verify --strict --deep "$new"
[[ "$("$new/Contents/MacOS/tclash-collector" -build-info)" == tclash-collector-device-reset-v1 ]] || {
  printf '%s\n' '安装包中的采集器不是当前版本。'
  exit 1
}
if [[ -d "$target" ]]; then /bin/mv -- "$target" "$stage/previous.app"; fi
/bin/mv -- "$new" "$target"
mkdir -p -- "$data"
/usr/bin/install -m 600 enrollment.json "$data/install-enrollment.json"
/usr/bin/open -a "$target"
