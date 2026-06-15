#!/usr/bin/env bash
# Reinstall Atlas Monitor when its sources have changed since the last install.
# Invoked automatically by the Claude Code Stop hook (see .claude/settings.json),
# so the installed app in the GNOME grid/dock always matches the latest code.
set -euo pipefail

cd "$(dirname "$(readlink -f "$0")")/.." || exit 1  # repo root, relative to this script
bin="${HOME}/.local/bin/atlas-monitor"

# Skip if the installed binary is already newer than every source file.
if [ -x "$bin" ] && [ -z "$(find . -path ./bin -prune -o \
        \( -name '*.go' -o -name '*.css' -o -name '*.svg' -o -name '*.desktop' \
           -o -name 'go.mod' -o -name 'go.sum' -o -name 'Makefile' \) \
        -newer "$bin" -print -quit 2>/dev/null)" ]; then
    exit 0
fi

make install >/tmp/atlas-monitor-reinstall.log 2>&1
