#!/usr/bin/env python3
"""Count real Claude Code user prompts in the last 5-hour window."""

import json
import os
import glob
from datetime import datetime, timezone, timedelta

WINDOW = timedelta(hours=5)

# 5-hour cycle prompt limits by tier
TIER_LIMITS = {
    "pro":    40,
    "max5x":  200,
    "max20x": 800,
}
DEFAULT_TIER = "max5x"

def is_real_prompt(entry):
    """A real prompt has type=user with string content (not tool_result dicts)."""
    if entry.get("type") != "user":
        return False
    msg = entry.get("message", {})
    if not isinstance(msg, dict) or msg.get("role") != "user":
        return False
    content = msg.get("content", "")
    # Real prompts have string content; tool results have list of dicts
    return isinstance(content, str) and len(content.strip()) > 0

def get_tier():
    conf = os.path.expanduser("~/.claude-tools.conf")
    if os.path.exists(conf):
        with open(conf) as f:
            for line in f:
                line = line.strip()
                if line.startswith("tier="):
                    return line.split("=", 1)[1].strip().lower()
    return DEFAULT_TIER

def main():
    tier = get_tier()
    limit = TIER_LIMITS.get(tier, TIER_LIMITS[DEFAULT_TIER])

    now = datetime.now(timezone.utc)
    cutoff = now - WINDOW
    cutoff_ts = cutoff.isoformat()

    prompts = []
    projects_dir = os.path.expanduser("~/.claude/projects")

    for jsonl in glob.glob(os.path.join(projects_dir, "**", "*.jsonl"), recursive=True):
        # Skip files not modified in last 5 hours (quick filter)
        mtime = os.path.getmtime(jsonl)
        if mtime < (now - WINDOW).timestamp():
            continue

        try:
            with open(jsonl, "r") as f:
                for line in f:
                    line = line.strip()
                    if not line or '"type":"user"' not in line:
                        continue
                    try:
                        entry = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    ts = entry.get("timestamp", "")
                    if ts < cutoff_ts:
                        continue
                    if is_real_prompt(entry):
                        prompts.append(ts)
        except (IOError, OSError):
            continue

    count = len(prompts)

    # Reset timer based on earliest prompt in window
    if prompts:
        earliest = min(prompts)
        try:
            earliest_dt = datetime.fromisoformat(earliest.replace("Z", "+00:00"))
            reset_at = earliest_dt + WINDOW
            remaining = (reset_at - now).total_seconds()
            if remaining <= 0:
                reset_str = "now"
            else:
                hrs = int(remaining // 3600)
                mins = int((remaining % 3600) // 60)
                reset_str = f"{hrs}h{mins:02d}m"
        except (ValueError, TypeError):
            reset_str = "--"
    else:
        reset_str = "5h00m"

    pct = int(count * 100 / limit) if limit > 0 else 0
    print(f"{count} {limit} {pct} {reset_str}")

if __name__ == "__main__":
    main()
