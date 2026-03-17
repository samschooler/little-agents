#!/usr/bin/env bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY="$SCRIPT_DIR/lila"

# Build if binary doesn't exist
if [ ! -f "$BINARY" ]; then
    echo "  Binary not found. Building..."
    if ! command -v go >/dev/null 2>&1; then
        # Try common Go install paths
        for p in /usr/local/go/bin/go /home/$USER/go/bin/go; do
            if [ -x "$p" ]; then
                GO="$p"
                break
            fi
        done
        if [ -z "$GO" ]; then
            echo "  Go is required to build. Install Go first."
            exit 1
        fi
    else
        GO=go
    fi
    (cd "$SCRIPT_DIR" && $GO build -o lila .)
fi

# Check dependencies
missing=()
command -v tmux >/dev/null 2>&1 || missing+=(tmux)
(command -v claude >/dev/null 2>&1 || command -v codex >/dev/null 2>&1) || missing+=("claude or codex")

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

ALIAS_LINE="alias lila='$BINARY'"
SSH_BLOCK="# Little Agents - auto-launch on SSH
if [ -n \"\$SSH_CONNECTION\" ]; then
    echo \"\"
    echo \"  Shortcuts:\"
    echo \"    lila  - little agents session manager\"
    echo \"    C-b d - detach from session\"
    echo \"\"
    $BINARY
    echo \"\"
fi"

# Remove old source-based installs
for pattern in "little-agents.sh" "claude-tools.sh" "little-agents/go/lila"; do
    if grep -qF "$pattern" "$RC_FILE" 2>/dev/null; then
        grep -vF "$pattern" "$RC_FILE" > "$RC_FILE.tmp"
        mv "$RC_FILE.tmp" "$RC_FILE"
        echo "  Removed old install referencing $pattern"
    fi
done

# Remove any old lila alias that points elsewhere
if grep -q "^alias lila=" "$RC_FILE" 2>/dev/null; then
    if ! grep -qF "$ALIAS_LINE" "$RC_FILE" 2>/dev/null; then
        grep -v "^alias lila=" "$RC_FILE" > "$RC_FILE.tmp"
        mv "$RC_FILE.tmp" "$RC_FILE"
        echo "  Removed old lila alias"
    fi
fi

# Add alias
if grep -qF "$ALIAS_LINE" "$RC_FILE" 2>/dev/null; then
    echo "  Already installed in $RC_FILE"
else
    {
        echo ""
        echo "# Little Agents - session manager + scheduler"
        echo "$ALIAS_LINE"
    } >> "$RC_FILE"
    echo "  Added alias to $RC_FILE"
fi

# Add SSH auto-launch if not present
if ! grep -qF "Little Agents - auto-launch on SSH" "$RC_FILE" 2>/dev/null; then
    echo "" >> "$RC_FILE"
    echo "$SSH_BLOCK" >> "$RC_FILE"
    echo "  Added SSH auto-launch to $RC_FILE"
fi

echo "  Installed. Restart your shell or run: source $RC_FILE"
