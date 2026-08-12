package web

import "testing"

// The GUI reported "completed · 67.4 GB" for a file that is 118 MB on disk,
// because the number came from MakeMKV's estimate for the title rather than
// from the file. Three of four Police Story 2 rips were reported that way.
func TestCompletedJobsReportTheFileThatLanded(t *testing.T) {
	if got := deliveredSize(8_912_345_678, "1013.7 MB"); got != "8.9 GB" {
		t.Errorf("deliveredSize = %q, want 8.9 GB", got)
	}
	if got := deliveredSize(123_456_789, "67.4 GB"); got != "123.5 MB" {
		t.Errorf("deliveredSize = %q, want 123.5 MB", got)
	}
}

// Jobs ripped before the column existed have nothing measured, and the estimate
// is better than a blank.
func TestJobsWithoutAMeasurementKeepTheEstimate(t *testing.T) {
	if got := deliveredSize(0, "67.4 GB"); got != "67.4 GB" {
		t.Errorf("deliveredSize = %q, want the estimate to stand in", got)
	}
}
