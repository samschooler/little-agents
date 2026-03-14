package main

import (
	"testing"
	"time"
)

func TestCronDescribe(t *testing.T) {
	tests := []struct {
		expr string
		want string
	}{
		{"* * * * *", "Every minute"},
		{"*/5 * * * *", "Every 5 minutes"},
		{"*/15 * * * *", "Every 15 minutes"},
		{"0 * * * *", "Every hour"},
		{"30 * * * *", "Every hour at :30"},
		{"0 8 * * *", "Daily at 8:00 AM"},
		{"0 0 * * *", "Daily at 12:00 AM"},
		{"0 12 * * *", "Daily at 12:00 PM"},
		{"0 13 * * *", "Daily at 1:00 PM"},
		{"30 9 * * *", "Daily at 9:30 AM"},
		{"0 8 * * 1-5", "Weekdays at 8:00 AM"},
		{"0 8 * * 0,6", "Weekends at 8:00 AM"},
		{"0 8 * * 1", "Mondays at 8:00 AM"},
		{"0 8 * * 0", "Sundays at 8:00 AM"},
		{"0 8 * * 5", "Fridays at 8:00 AM"},
		{"0 8 1 * *", "1st of every month at 8:00 AM"},
		{"0 8 1 6 *", "0 8 1 6 *"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got := cronDescribe(tt.expr)
			if got != tt.want {
				t.Errorf("cronDescribe(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestDatetimeToCron(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-03-15 14:00", "0 14 15 3 *"},
		{"2026-12-25 09:30", "30 9 25 12 *"},
		{"2026-01-01 00:00", "0 0 1 1 *"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ts, err := time.Parse("2006-01-02 15:04", tt.input)
			if err != nil {
				t.Fatal(err)
			}
			got := datetimeToCron(ts)
			if got != tt.want {
				t.Errorf("datetimeToCron(%v) = %q, want %q", ts, got, tt.want)
			}
		})
	}
}
