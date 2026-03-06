#!/usr/bin/env python3
"""Track Claude Code token usage in the current 5-hour billing block.

Reads assistant message usage data from ~/.claude/projects/**/*.jsonl,
groups into 5h blocks (matching Claude's billing windows), and reports
token totals for the active block.

Based on how ryoppippi/ccusage calculates session blocks.
"""

import json
import os
import glob
from datetime import datetime, timezone, timedelta

WINDOW_HOURS = 5
WINDOW = timedelta(hours=WINDOW_HOURS)

# Approximate 5h token limits by tier (calibrated against claude.ai dashboard)
TIER_LIMITS = {
    "pro":    45_000_000,   # ~45M
    "max5x":  120_000_000,  # ~120M
    "max20x": 480_000_000,  # ~480M
}


def floor_to_hour(dt):
    return dt.replace(minute=0, second=0, microsecond=0)


def get_tier():
    """Auto-detect subscription tier from Claude credentials."""
    creds = os.path.expanduser("~/.claude/.credentials.json")
    if os.path.exists(creds):
        try:
            with open(creds) as f:
                data = json.load(f)
            oauth = data.get("claudeAiOauth", {})
            rate_tier = oauth.get("rateLimitTier", "")
            if "20x" in rate_tier:
                return "max20x"
            elif "5x" in rate_tier:
                return "max5x"
            sub_type = oauth.get("subscriptionType", "")
            if sub_type == "pro":
                return "pro"
        except (json.JSONDecodeError, IOError):
            pass
    conf = os.path.expanduser("~/.claude-tools.conf")
    if os.path.exists(conf):
        try:
            with open(conf) as f:
                for line in f:
                    line = line.strip()
                    if line.startswith("tier="):
                        return line.split("=", 1)[1].strip().lower()
        except IOError:
            pass
    return "max5x"


def load_entries():
    """Load all assistant usage entries from recent JSONL files."""
    now = datetime.now(timezone.utc)
    cutoff = now - timedelta(hours=WINDOW_HOURS * 2)
    cutoff_ts = cutoff.isoformat()
    entries = []
    projects_dir = os.path.expanduser("~/.claude/projects")

    for jsonl in glob.glob(os.path.join(projects_dir, "**", "*.jsonl"), recursive=True):
        mtime = os.path.getmtime(jsonl)
        if mtime < cutoff.timestamp():
            continue
        try:
            with open(jsonl, "r") as f:
                for line in f:
                    line = line.strip()
                    if not line or '"type":"assistant"' not in line:
                        continue
                    try:
                        entry = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    if entry.get("type") != "assistant":
                        continue
                    ts = entry.get("timestamp", "")
                    if ts < cutoff_ts:
                        continue
                    msg = entry.get("message", {})
                    if not isinstance(msg, dict):
                        continue
                    usage = msg.get("usage")
                    if not usage:
                        continue
                    inp = usage.get("input_tokens", 0)
                    out = usage.get("output_tokens", 0)
                    cache_create = usage.get("cache_creation_input_tokens", 0)
                    cache_read = usage.get("cache_read_input_tokens", 0)
                    total = inp + out + cache_create + cache_read
                    if total == 0:
                        continue
                    entries.append({"timestamp": ts, "total": total})
        except (IOError, OSError):
            continue

    entries.sort(key=lambda e: e["timestamp"])
    return entries


def find_active_block(entries):
    """Find the active 5h block."""
    if not entries:
        return None

    now = datetime.now(timezone.utc)
    window_ms = WINDOW_HOURS * 3600

    block_start = None
    block_entries = []

    for entry in entries:
        ts_str = entry["timestamp"]
        try:
            ts = datetime.fromisoformat(ts_str.replace("Z", "+00:00"))
        except (ValueError, TypeError):
            continue
        epoch = ts.timestamp()

        if block_start is None:
            block_start = floor_to_hour(ts).timestamp()
            block_entries = [entry]
        else:
            since_start = epoch - block_start
            last_ts = datetime.fromisoformat(
                block_entries[-1]["timestamp"].replace("Z", "+00:00")
            ).timestamp()
            since_last = epoch - last_ts

            if since_start > window_ms or since_last > window_ms:
                block_start = floor_to_hour(ts).timestamp()
                block_entries = [entry]
            else:
                block_entries.append(entry)

    if block_start is None:
        return None

    block_end = block_start + window_ms
    now_epoch = now.timestamp()

    if block_entries:
        last_entry_ts = datetime.fromisoformat(
            block_entries[-1]["timestamp"].replace("Z", "+00:00")
        ).timestamp()
        is_active = (now_epoch - last_entry_ts < window_ms) and (now_epoch < block_end)
    else:
        is_active = False

    if not is_active:
        return None

    return {"start": block_start, "end": block_end, "entries": block_entries}


def fmt(n):
    if n >= 1_000_000:
        return f"{n / 1_000_000:.1f}M"
    elif n >= 1_000:
        return f"{n / 1_000:.0f}K"
    return str(n)


def main():
    tier = get_tier()
    limit = TIER_LIMITS.get(tier, TIER_LIMITS["max5x"])

    entries = load_entries()
    active = find_active_block(entries)

    now = datetime.now(timezone.utc)

    if active is None:
        print("0 0 5h00m")
        return

    total_tokens = sum(e["total"] for e in active["entries"])

    remaining_secs = active["end"] - now.timestamp()
    if remaining_secs <= 0:
        reset_str = "now"
    else:
        hrs = int(remaining_secs // 3600)
        mins = int((remaining_secs % 3600) // 60)
        reset_str = f"{hrs}h{mins:02d}m"

    pct = int(total_tokens * 100 / limit) if limit > 0 else 0

    print(f"{fmt(total_tokens)} {pct} {reset_str}")


if __name__ == "__main__":
    main()
