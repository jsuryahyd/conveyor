#!/usr/bin/env bash
# Removes the conveyor CLI, Conveyor.app, its skills and its ~/.claude hooks.
# First saves settings, apps, automations and user agents (no repos) to
# ~/conveyor-state.json and $CONVEYOR_HOME/state.json, which the next install imports.
# The data folder ($CONVEYOR_HOME, default ~/.conveyor) is left in place.
set -euo pipefail

cd "$(dirname "$0")/.."
BIN_DIR=${BIN_DIR:-$HOME/.local/bin}
cv="$BIN_DIR/conveyor"

if [ -x "$cv" ]; then
	"$cv" state export "$HOME/conveyor-state.json"
	cp "$HOME/conveyor-state.json" "${CONVEYOR_HOME:-$HOME/.conveyor}/state.json"
	"$cv" hooks uninstall || true
fi
for d in skills/*/; do
	rm -rf "$HOME/.claude/skills/$(basename "$d")"
	rm -rf "$HOME/.gemini/config/skills/$(basename "$d")"
done

if [ "$(uname)" = "Darwin" ]; then
	APP_DIR=${APP_DIR:-/Applications}
	rm -rf "$APP_DIR/Conveyor.app"
else
	APP_DIR=${APP_DIR:-$HOME/.local/share/applications}
	rm -f "$BIN_DIR/Conveyor" "$APP_DIR/conveyor.desktop" "$APP_DIR/Conveyor"
fi
rm -f "$cv"
echo "uninstalled; data kept in ${CONVEYOR_HOME:-$HOME/.conveyor}"
