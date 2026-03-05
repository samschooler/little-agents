#!/usr/bin/env bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SOURCE_LINE="source \"$SCRIPT_DIR/claude-tools.sh\""

# Check dependencies
missing=""
command -v python3 >/dev/null 2>&1 || missing="python3"
command -v tmux >/dev/null 2>&1   || missing="$missing tmux"
command -v claude >/dev/null 2>&1 || missing="$missing claude"

if [ -n "$missing" ]; then
    echo "  Missing dependencies:$missing"
    echo "  Install them first, then re-run."
    exit 1
fi

# Detect shell rc file
if [ -n "$ZSH_VERSION" ] || [ "$(basename "$SHELL")" = "zsh" ]; then
    RC_FILE="$HOME/.zshrc"
else
    RC_FILE="$HOME/.bashrc"
fi

# Check if already installed
if grep -qF "claude-tools.sh" "$RC_FILE" 2>/dev/null; then
    # Update existing source line to point to current location
    if grep -qF "$SOURCE_LINE" "$RC_FILE"; then
        echo "  Already installed in $RC_FILE"
    else
        # Remove old source line, add new one
        grep -vF "claude-tools.sh" "$RC_FILE" > "$RC_FILE.tmp"
        mv "$RC_FILE.tmp" "$RC_FILE"
        echo "$SOURCE_LINE" >> "$RC_FILE"
        echo "  Updated source path in $RC_FILE"
    fi
else
    echo "" >> "$RC_FILE"
    echo "# Claude Tools - tmux session manager + quota tracker" >> "$RC_FILE"
    echo "$SOURCE_LINE" >> "$RC_FILE"
    echo "  Added to $RC_FILE"
fi

echo "  Installed. Restart your shell or run: source $RC_FILE"
