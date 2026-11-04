package web

import (
	"math"
	"testing"
	"time"
)

func TestCompletedCopyRate(t *testing.T) {
	start := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	end := start.Add(1000 * time.Second)

	tests := []struct {
		name   string
		status string
		output int64
		start  time.Time
		end    time.Time
		want   float64
	}{
		{"file over copy time", "completed", 38_400_000_000, start, end, 38_400_000},
		{"failed job", "failed", 38_400_000_000, start, end, 0},
		{"no file measured", "completed", 0, start, end, 0},
		// Jobs from before the copy was recorded. created_at to updated_at also
		// covers the queue, the analysis and the move, so nothing is shown.
		{"no copy start", "completed", 38_400_000_000, time.Time{}, end, 0},
		{"no copy end", "completed", 38_400_000_000, start, time.Time{}, 0},
		{"end before start", "completed", 38_400_000_000, end, start, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := completedCopyRate(tt.status, tt.output, tt.start, tt.end)
			if math.Abs(got-tt.want) > 0.5 {
				t.Errorf("completedCopyRate = %v, want %v", got, tt.want)
			}
		})
	}
}
