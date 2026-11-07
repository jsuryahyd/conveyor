#!/usr/bin/env bash
# Installs the conveyor CLI and the Conveyor desktop app.
# BIN_DIR overrides where the CLI goes (default ~/.local/bin, where claude installs itself);
# APP_DIR overrides where Conveyor.app goes (default /Applications).
set -euo pipefail

cd "$(dirname "$0")/.."
BIN_DIR=${BIN_DIR:-$HOME/.local/bin}
if [ "$(uname)" = "Darwin" ]; then
	APP_DIR=${APP_DIR:-/Applications}
else
	APP_DIR=${APP_DIR:-$HOME/.local/share/applications}
fi
user_path=$PATH
export PATH="$PATH:$(go env GOPATH)/bin"

for c in go git npm; do
	command -v "$c" >/dev/null || { echo "missing: $c" >&2; exit 1; }
done
if ! command -v claude >/dev/null && ! command -v agy >/dev/null; then
	echo "missing: either claude or agy CLI must be installed" >&2
	exit 1
fi
command -v gh >/dev/null || echo "warning: gh not found; PR tracking will not work" >&2
command -v wails >/dev/null || go install "github.com/wailsapp/wails/v2/cmd/wails@$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)"

echo "==> CLI"
mkdir -p "$BIN_DIR"
GOBIN="$BIN_DIR" go install ./cmd/conveyor

echo "==> App"
git fetch --tags --quiet || echo "warning: could not fetch tags; version may be stale" >&2
wails build -clean -ldflags "-X github.com/JayaSuryaOolio/conveyor/internal/ui.Version=$(git describe --tags --always --dirty)"
git checkout -- frontend/dist/.gitkeep 2>/dev/null || true
git checkout -- frontend/package.json.md5 2>/dev/null || true # rewritten on every build; frontend/wailsjs is gitignored
if [ -d build/bin/Conveyor.app ]; then
	mkdir -p "$APP_DIR"
	rm -rf "$APP_DIR/Conveyor.app"
	cp -R build/bin/Conveyor.app "$APP_DIR/"
	echo "installed: $APP_DIR/Conveyor.app"
elif [ -f build/bin/Conveyor ]; then
	mkdir -p "$BIN_DIR"
	cp --remove-destination build/bin/Conveyor "$BIN_DIR/"
	echo "installed: $BIN_DIR/Conveyor"
	if [ -d "$APP_DIR" ]; then
		[ -f "$APP_DIR/Conveyor" ] && rm -f "$APP_DIR/Conveyor"
		cat > "$APP_DIR/conveyor.desktop" <<EOF
[Desktop Entry]
Name=Conveyor
Comment=Local kanban that orchestrates agentic tasks
Exec=$BIN_DIR/Conveyor
Icon=$PWD/build/appicon.png
Terminal=false
Type=Application
Categories=Development;
EOF
		echo "installed: $APP_DIR/conveyor.desktop"
	fi
fi

echo "==> Skills"
# Conveyor's user-level skills; overwritten on every install, so edit them in this repo.
mkdir -p "$HOME/.claude/skills"
for d in skills/*/; do
	rm -rf "$HOME/.claude/skills/$(basename "$d")"
	cp -R "${d%/}" "$HOME/.claude/skills/" # no trailing slash: BSD cp would copy the contents only
	echo "installed: ~/.claude/skills/$(basename "$d")"
done

if command -v agy >/dev/null || [ -d "$HOME/.gemini" ]; then
	mkdir -p "$HOME/.gemini/config/skills"
	for d in skills/*/; do
		rm -rf "$HOME/.gemini/config/skills/$(basename "$d")"
		cp -R "${d%/}" "$HOME/.gemini/config/skills/"
		echo "installed: ~/.gemini/config/skills/$(basename "$d")"
	done
fi

if [ -d "$HOME/.claude/agents" ]; then
	mkdir -p "$HOME/.gemini/config/agents"
	for f in "$HOME/.claude/agents"/*.md; do
		if [ -f "$f" ]; then
			cp "$f" "$HOME/.gemini/config/agents/"
		fi
	done
fi

echo "==> Weekly doctor"
# A weekly task, on the repo registered for this checkout, that runs the conveyor-doctor skill.
repo=$("$BIN_DIR/conveyor" repo list --json | python3 -c 'import json,os,sys; p=os.path.realpath(sys.argv[1]); print(next((r["name"] for r in json.load(sys.stdin) or [] if os.path.realpath(r["path"])==p), ""))' "$PWD")
if [ -z "$repo" ]; then
	echo "skipped: register this checkout first (conveyor repo add $PWD), then re-run"
elif "$BIN_DIR/conveyor" automation list | grep -q '^conveyor-doctor '; then
	echo "exists: automation conveyor-doctor"
else
	"$BIN_DIR/conveyor" automation add conveyor-doctor --schedule "0 9 * * 1" --kind create_task \
		--arg "{\"title\":\"Conveyor doctor: weekly run review\",\"description\":\"Use the conveyor-doctor skill to review last week's runs and propose prompt and context fixes.\",\"repos\":[\"$repo\"],\"start\":true}"
fi

echo "==> Saved state"
# Left by scripts/uninstall.sh (or copied from another machine); renamed once imported so it is not re-applied.
state="${CONVEYOR_HOME:-$HOME/.conveyor}/state.json"
if [ -f "$state" ]; then
	echo "found $state from a previous install; importing settings, apps, automations and agents (existing ones are kept):"
	"$BIN_DIR/conveyor" state import "$state" && mv "$state" "$state.imported"
else
	echo "none"
fi

echo "installed: $BIN_DIR/conveyor"
case ":$user_path:" in
*":$BIN_DIR:"*) ;;
*) echo "note: add $BIN_DIR to your PATH; the app and hooks find conveyor through it" ;;
esac
