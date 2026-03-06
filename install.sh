#!/usr/bin/env bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SOURCE_LINE="source \"$SCRIPT_DIR/little-agents.sh\""

# Check dependencies
missing=()
command -v python3 >/dev/null 2>&1 || missing+=(python3)
command -v tmux >/dev/null 2>&1   || missing+=(tmux)
command -v claude >/dev/null 2>&1 || missing+=(claude)

if [ ${#missing[@]} -gt 0 ]; then
    echo "  Missing dependencies: ${missing[*]}"
    echo "  Install them first, then re-run."
    exit 1
fi

# Detect shell rc file
if [ -n "$ZSH_VERSION" ] || [ "$(basename "$SHELL")" = "zsh" ]; then
    RC_FILE="$HOME/.zshrc"
else
    RC_FILE="$HOME/.bashrc"
fi

# Check if already installed (also handles upgrade from old claude-tools.sh name)
if grep -qF "little-agents.sh" "$RC_FILE" 2>/dev/null; then
    if grep -qF "$SOURCE_LINE" "$RC_FILE"; then
        echo "  Already installed in $RC_FILE"
    else
        grep -vF "little-agents.sh" "$RC_FILE" > "$RC_FILE.tmp"
        mv "$RC_FILE.tmp" "$RC_FILE"
        echo "$SOURCE_LINE" >> "$RC_FILE"
        echo "  Updated source path in $RC_FILE"
    fi
elif grep -qF "claude-tools.sh" "$RC_FILE" 2>/dev/null; then
    # Upgrade from old name
    grep -vF "claude-tools.sh" "$RC_FILE" > "$RC_FILE.tmp"
    mv "$RC_FILE.tmp" "$RC_FILE"
    echo "$SOURCE_LINE" >> "$RC_FILE"
    echo "  Upgraded from claude-tools to little-agents in $RC_FILE"
else
    echo "" >> "$RC_FILE"
    echo "# Little Agents - tmux session manager + quota tracker" >> "$RC_FILE"
    echo "$SOURCE_LINE" >> "$RC_FILE"
    echo "  Added to $RC_FILE"
fi

echo "  Installed. Restart your shell or run: source $RC_FILE"
