# Little Agents

Lightweight tmux session manager and quota tracker for [Claude Code](https://docs.anthropic.com/en/docs/claude-code).

```
    q) hyper [hyper] ◉ waiting
    w) api   [api]   💭 thinking

  ⚡7.0M (5%) resets 4h46m
  [q-w] attach  [k] kill  [n] new  [c] cli:claude  [esc] quit
```

## Commands

- **`cst`** — live interactive session manager (attach, kill, create, quota, toggle launcher)
- **`cld`** — alias for `claude --dangerously-skip-permissions`

Inside `cst`, press `c` to switch the launcher between `claude` and `codex` (persisted). Press `n` to create a new session, `k` to kill, or any session key to attach.

## Unread indicator

A red `●` dot appears before the session name when an agent finishes working (transitions from thinking/tool use to waiting) and you haven't attached to that session yet. Attaching to the session clears it. If you're already attached when the agent completes, no dot is shown.

## Quota tracking

Tracks real token usage (input, output, cache) from assistant messages in `~/.claude/projects/**/*.jsonl`, grouped into 5-hour billing blocks matching Claude's billing windows. Based on how [ccusage](https://github.com/ryoppippi/ccusage) calculates session blocks.

Auto-detects your subscription tier from `~/.claude/.credentials.json`:

| Tier | ~Token limit / 5h |
|------|-------------------|
| Pro | 45M |
| Max 5x | 120M |
| Max 20x | 480M |

Color coding: green < 50%, yellow 50–79%, red 80%+.

## Install

Requires `python3`, `tmux`, and `claude`.

```bash
git clone https://github.com/samschooler/little-agents.git
cd little-agents
./install.sh
```

Adds one `source` line to `.bashrc` (Linux) or `.zshrc` (macOS). Your subscription tier is auto-detected from `~/.claude/.credentials.json`.

## Uninstall

Remove the `source` line from your shell rc file and delete the directory.
