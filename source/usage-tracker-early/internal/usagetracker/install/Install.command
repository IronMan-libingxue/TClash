#!/bin/zsh
set -eu
umask 077
cd -- "${0:A:h}"
target="$HOME/Applications/Codex 用量 B.app"
if [[ ! -d "$target" ]]; then
  mkdir -p -- "$HOME/Applications"
  staging=$(mktemp -d -t codex-usage-install)
  /usr/bin/ditto -x -k 'Codex Usage Mac.zip' "$staging"
  /bin/mv -- "$staging/Codex 用量.app" "$target"
  /bin/rmdir -- "$staging"
fi
data="$HOME/Library/Application Support/Codex Usage Tracker"
mkdir -p -- "$data"
/usr/bin/install -m 600 enrollment.json "$data/install-enrollment.json"
/usr/bin/open -a "$target" --args -install-login
