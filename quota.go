package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const windowHours = 5

var tierLimits = map[string]int64{
	"pro":    45_000_000,
	"max5x":  120_000_000,
	"max20x": 480_000_000,
}

type usageEntry struct {
	Timestamp string `json:"timestamp"`
	Total     int64
}

type activeBlock struct {
	Start   float64
	End     float64
	Entries []usageEntry
}

type quotaResult struct {
	Formatted string
	Pct       int
	ResetStr  string
}

func floorToHour(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}

func getTier() string {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return "max5x"
	}
	var creds map[string]interface{}
	if err := json.Unmarshal(data, &creds); err != nil {
		return "max5x"
	}
	oauth, ok := creds["claudeAiOauth"].(map[string]interface{})
	if !ok {
		return "max5x"
	}
	rateTier, _ := oauth["rateLimitTier"].(string)
	if strings.Contains(rateTier, "20x") {
		return "max20x"
	}
	if strings.Contains(rateTier, "5x") {
		return "max5x"
	}
	subType, _ := oauth["subscriptionType"].(string)
	if subType == "pro" {
		return "pro"
	}
	return "max5x"
}

func loadEntries() []usageEntry {
	home, _ := os.UserHomeDir()
	now := time.Now().UTC()
	cutoff := now.Add(-time.Duration(windowHours*2) * time.Hour)
	cutoffTS := cutoff.Format(time.RFC3339)
	cutoffUnix := cutoff.Unix()

	projectsDir := filepath.Join(home, ".claude", "projects")
	var entries []usageEntry

	_ = filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info.ModTime().Unix() < cutoffUnix {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !strings.Contains(line, `"type":"assistant"`) {
				continue
			}
			var entry map[string]interface{}
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				continue
			}
			if entry["type"] != "assistant" {
				continue
			}
			ts, _ := entry["timestamp"].(string)
			if ts < cutoffTS {
				continue
			}
			msg, ok := entry["message"].(map[string]interface{})
			if !ok {
				continue
			}
			usage, ok := msg["usage"].(map[string]interface{})
			if !ok {
				continue
			}
			inp := jsonInt(usage, "input_tokens")
			out := jsonInt(usage, "output_tokens")
			cacheCreate := jsonInt(usage, "cache_creation_input_tokens")
			cacheRead := jsonInt(usage, "cache_read_input_tokens")
			total := inp + out + cacheCreate + cacheRead
			if total == 0 {
				continue
			}
			entries = append(entries, usageEntry{Timestamp: ts, Total: total})
		}
		return nil
	})

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp < entries[j].Timestamp
	})
	return entries
}

func jsonInt(m map[string]interface{}, key string) int64 {
	v, ok := m[key].(float64)
	if !ok {
		return 0
	}
	return int64(v)
}

func parseTS(s string) (time.Time, error) {
	s = strings.Replace(s, "Z", "+00:00", 1)
	return time.Parse(time.RFC3339, s)
}

func findActiveBlock(entries []usageEntry) *activeBlock {
	if len(entries) == 0 {
		return nil
	}
	now := time.Now().UTC()
	windowSecs := float64(windowHours * 3600)

	var blockStart float64
	var blockEntries []usageEntry
	started := false

	for _, e := range entries {
		ts, err := parseTS(e.Timestamp)
		if err != nil {
			continue
		}
		epoch := float64(ts.Unix())

		if !started {
			blockStart = float64(floorToHour(ts).Unix())
			blockEntries = []usageEntry{e}
			started = true
		} else {
			sinceStart := epoch - blockStart
			lastTS, _ := parseTS(blockEntries[len(blockEntries)-1].Timestamp)
			sinceLast := epoch - float64(lastTS.Unix())

			if sinceStart > windowSecs || sinceLast > windowSecs {
				blockStart = float64(floorToHour(ts).Unix())
				blockEntries = []usageEntry{e}
			} else {
				blockEntries = append(blockEntries, e)
			}
		}
	}

	if !started {
		return nil
	}

	blockEnd := blockStart + windowSecs
	nowEpoch := float64(now.Unix())

	if len(blockEntries) > 0 {
		lastTS, _ := parseTS(blockEntries[len(blockEntries)-1].Timestamp)
		lastEpoch := float64(lastTS.Unix())
		isActive := (nowEpoch-lastEpoch < windowSecs) && (nowEpoch < blockEnd)
		if !isActive {
			return nil
		}
	} else {
		return nil
	}

	return &activeBlock{Start: blockStart, End: blockEnd, Entries: blockEntries}
}

func fmtTokens(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.0fK", math.Round(float64(n)/1_000))
	}
	return fmt.Sprintf("%d", n)
}

func getQuota() quotaResult {
	tier := getTier()
	limit := tierLimits[tier]

	entries := loadEntries()
	active := findActiveBlock(entries)

	if active == nil {
		return quotaResult{Formatted: "0", Pct: 0, ResetStr: "5h00m"}
	}

	var totalTokens int64
	for _, e := range active.Entries {
		totalTokens += e.Total
	}

	now := time.Now().UTC()
	remainingSecs := active.End - float64(now.Unix())
	var resetStr string
	if remainingSecs <= 0 {
		resetStr = "now"
	} else {
		hrs := int(remainingSecs) / 3600
		mins := (int(remainingSecs) % 3600) / 60
		resetStr = fmt.Sprintf("%dh%02dm", hrs, mins)
	}

	pct := 0
	if limit > 0 {
		pct = int(totalTokens * 100 / limit)
	}

	return quotaResult{Formatted: fmtTokens(totalTokens), Pct: pct, ResetStr: resetStr}
}
