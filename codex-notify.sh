#!/bin/bash
# Codex notify hook: writes status to /tmp/claude-status-<tmux_session>
# Install: add to ~/.codex/config.toml:
#   notify = ["/path/to/codex-notify.sh"]

[ -z "$TMUX" ] && exit 0

session=$(tmux display-message -p '#{session_name}' 2>/dev/null)
[ -z "$session" ] && exit 0

status_file="/tmp/claude-status-${session}"

# Codex passes JSON as first argument
json="${1:-}"
event_type=$(echo "$json" | jq -r '.type // empty' 2>/dev/null)

case "$event_type" in
    agent-turn-complete)
        echo "waiting" > "$status_file"
        ;;
esac
