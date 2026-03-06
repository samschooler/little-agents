#!/usr/bin/env python3
"""Track Claude Code token usage in the current 5-hour billing block.

Reads assistant message usage data from ~/.claude/projects/**/*.jsonl,
groups into 5h blocks (matching Claude's billing windows), and reports
token totals for the active block with percentage against peak usage.

Based on how ryoppippi/ccusage calculates session blocks.
"""

import json
import os
import glob
from datetime import datetime, timezone, timedelta

WINDOW_HOURS = 5
WINDOW = timedelta(hours=WINDOW_HOURS)
PEAK_FILE = os.path.expanduser("~/.claude-tools-peak")


def floor_to_hour(dt):
    return dt.replace(minute=0, second=0, microsecond=0)


def load_entries():
    """Load all assistant usage entries from recent JSONL files."""
    now = datetime.now(timezone.utc)
    cutoff = now - timedelta(days=7)  # Look back a week for peak calculation
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


def find_blocks(entries):
    """Find all 5h blocks. Returns (completed_blocks, active_block)."""
    if not entries:
        return [], None

    now = datetime.now(timezone.utc)
    window_ms = WINDOW_HOURS * 3600

    blocks = []
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
                blocks.append({"start": block_start, "end": block_start + window_ms, "entries": block_entries})
                block_start = floor_to_hour(ts).timestamp()
                block_entries = [entry]
            else:
                block_entries.append(entry)

    # Last block
    if block_start is not None and block_entries:
        block_end = block_start + window_ms
        now_epoch = now.timestamp()
        last_entry_ts = datetime.fromisoformat(
            block_entries[-1]["timestamp"].replace("Z", "+00:00")
        ).timestamp()
        is_active = (now_epoch - last_entry_ts < window_ms) and (now_epoch < block_end)

        blk = {"start": block_start, "end": block_end, "entries": block_entries}
        if is_active:
            return blocks, blk
        else:
            blocks.append(blk)

    return blocks, None


def get_peak(completed_blocks):
    """Get peak token usage from completed blocks, persisted to disk."""
    # Calculate peak from completed blocks
    current_peak = 0
    for blk in completed_blocks:
        total = sum(e["total"] for e in blk["entries"])
        if total > current_peak:
            current_peak = total

    # Load saved peak
    saved_peak = 0
    if os.path.exists(PEAK_FILE):
        try:
            with open(PEAK_FILE) as f:
                saved_peak = int(f.read().strip())
        except (ValueError, IOError):
            pass

    peak = max(current_peak, saved_peak)

    # Save if new peak
    if peak > saved_peak:
        try:
            with open(PEAK_FILE, "w") as f:
                f.write(str(peak))
        except IOError:
            pass

    return peak


def fmt(n):
    if n >= 1_000_000:
        return f"{n / 1_000_000:.1f}M"
    elif n >= 1_000:
        return f"{n / 1_000:.0f}K"
    return str(n)


def main():
    entries = load_entries()
    completed, active = find_blocks(entries)
    peak = get_peak(completed)

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

    pct = int(total_tokens * 100 / peak) if peak > 0 else 0

    # Output: total_formatted pct reset_time
    print(f"{fmt(total_tokens)} {pct} {reset_str}")


if __name__ == "__main__":
    main()
