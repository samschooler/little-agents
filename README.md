# Little Agents

Lightweight tmux session manager and quota tracker for [Claude Code](https://docs.anthropic.com/en/docs/claude-code).

```
  ⚡42 prompts/5h (resets 3h15m)
  Sessions:
    api [api] ◉ waiting
    web [web] 💭 thinking
```

## What it does

- **`cs`** — show all Claude Code tmux sessions with live status + 5-hour quota usage
- **`cst`** — interactive session manager (attach, kill, create — refreshes live)
- **`nt <name>`** — create a new tmux session with repo picker
- **`kt <name>`** — kill a tmux session
- **`at <name>`** — attach to a tmux session
- **`cld`** — alias for `claude --dangerously-skip-permissions`

## Quota tracking

The quota line shows your real prompt count in the rolling 5-hour window by scanning `~/.claude/projects/` conversation files. It filters out tool results and system messages to count only actual human prompts.

Color changes as you approach limits:
- 🟢 Green: < 100 prompts
- 🟡 Yellow: 100–159 prompts
- 🔴 Red: 160+ prompts

## Install

Requires `python3`, `tmux`, and `claude`.

```bash
git clone https://github.com/samschooler/little-agents.git
cd little-agents
./install.sh
```

This adds one `source` line to your `.bashrc` (Linux) or `.zshrc` (macOS). That's it.

## Uninstall

Remove the `source` line from your shell rc file and delete the directory.
