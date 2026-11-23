#!/bin/zsh
set -eu
umask 077
cd -- "${0:A:h}"
target="$HOME/Applications/TClash Preview.app"
if pgrep -f "$target/Contents/MacOS/clash-verge" >/dev/null; then
  printf '%s\n' '请先退出 TClash Preview，再重新打开此安装程序。'
  exit 1
fi
if [[ ! -d "$target" ]]; then
  mkdir -p -- "$HOME/Applications"
  stage=$(mktemp -d -t tclash-install)
  /usr/bin/ditto -x -k 'TClash Preview Mac.zip' "$stage"
  /bin/mv -- "$stage/TClash Preview.app" "$target"
  /bin/rmdir -- "$stage"
fi
data="$HOME/Library/Application Support/TClash Preview/collector"
mkdir -p -- "$data"
/usr/bin/install -m 600 enrollment.json "$data/install-enrollment.json"
/usr/bin/open -a "$target"
