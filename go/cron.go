package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var dayNames = []string{"Sundays", "Mondays", "Tuesdays", "Wednesdays", "Thursdays", "Fridays", "Saturdays"}

func cronDescribe(expr string) string {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return expr
	}
	minute, hour, dom, month, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

	if minute == "*" && hour == "*" && dom == "*" && month == "*" && dow == "*" {
		return "Every minute"
	}

	if strings.HasPrefix(minute, "*/") && hour == "*" && dom == "*" && month == "*" && dow == "*" {
		return fmt.Sprintf("Every %s minutes", minute[2:])
	}

	if hour == "*" && dom == "*" && month == "*" && dow == "*" {
		if minute == "0" {
			return "Every hour"
		}
		m, err := strconv.Atoi(minute)
		if err == nil {
			return fmt.Sprintf("Every hour at :%02d", m)
		}
		return expr
	}

	h, hErr := strconv.Atoi(hour)
	m, mErr := strconv.Atoi(minute)
	if hErr != nil || mErr != nil {
		return expr
	}
	timeStr := formatTimeAMPM(h, m)

	if dom == "*" && month == "*" {
		if dow == "*" {
			return fmt.Sprintf("Daily at %s", timeStr)
		}
		if dow == "1-5" {
			return fmt.Sprintf("Weekdays at %s", timeStr)
		}
		if dow == "0,6" || dow == "6,0" {
			return fmt.Sprintf("Weekends at %s", timeStr)
		}
		d, err := strconv.Atoi(dow)
		if err == nil && d >= 0 && d <= 6 {
			return fmt.Sprintf("%s at %s", dayNames[d], timeStr)
		}
		return expr
	}

	if month == "*" && dow == "*" {
		d, err := strconv.Atoi(dom)
		if err == nil {
			return fmt.Sprintf("%s of every month at %s", ordinal(d), timeStr)
		}
	}

	return expr
}

func formatTimeAMPM(h, m int) string {
	ampm := "AM"
	if h >= 12 {
		ampm = "PM"
	}
	display := h % 12
	if display == 0 {
		display = 12
	}
	return fmt.Sprintf("%d:%02d %s", display, m, ampm)
}

func ordinal(n int) string {
	suffix := "th"
	switch n % 10 {
	case 1:
		if n%100 != 11 {
			suffix = "st"
		}
	case 2:
		if n%100 != 12 {
			suffix = "nd"
		}
	case 3:
		if n%100 != 13 {
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

func datetimeToCron(t time.Time) string {
	return fmt.Sprintf("%d %d %d %d *", t.Minute(), t.Hour(), t.Day(), int(t.Month()))
}
