package ripper

import (
	"context"
	"testing"
)

func TestJobStatusTransitions(t *testing.T) {
	job := NewJob(0, 1, "DISC_NAME", "/output")

	// Initial state
	if job.Status != StatusPending {
		t.Fatalf("expected status %q, got %q", StatusPending, job.Status)
	}
	if !job.StartedAt.IsZero() {
		t.Error("StartedAt should be zero before Start()")
	}

	// Start
	job.Start()
	if job.Status != StatusRipping {
		t.Fatalf("expected status %q after Start(), got %q", StatusRipping, job.Status)
	}
	if job.StartedAt.IsZero() {
		t.Error("StartedAt should be set after Start()")
	}

	// UpdateProgress
	job.UpdateProgress(42)
	if job.Progress != 42 {
		t.Fatalf("expected progress 42, got %d", job.Progress)
	}

	// Complete
	job.Complete("/output/movie.mkv")
	if job.Status != StatusCompleted {
		t.Fatalf("expected status %q after Complete(), got %q", StatusCompleted, job.Status)
	}
	if job.Progress != 100 {
		t.Fatalf("expected progress 100 after Complete(), got %d", job.Progress)
	}
	if job.OutputPath != "/output/movie.mkv" {
		t.Fatalf("expected OutputPath %q, got %q", "/output/movie.mkv", job.OutputPath)
	}
	if job.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set after Complete()")
	}
}

func TestJobFail(t *testing.T) {
	job := NewJob(0, 1, "DISC_NAME", "/output")
	job.Start()
	job.Fail("disc read error")

	if job.Status != StatusFailed {
		t.Fatalf("expected status %q after Fail(), got %q", StatusFailed, job.Status)
	}
	if job.Error != "disc read error" {
		t.Fatalf("expected Error %q, got %q", "disc read error", job.Error)
	}
	if job.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set after Fail()")
	}
}

func TestJobSkip(t *testing.T) {
	job := NewJob(0, 1, "DISC", "/output")
	job.Skip()

	if job.Status != StatusSkipped {
		t.Fatalf("expected status %q after Skip(), got %q", StatusSkipped, job.Status)
	}
	if job.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set after Skip()")
	}
}

func TestJobCancel_WithCancelFunc(t *testing.T) {
	job := NewJob(0, 1, "DISC", "/output")
	ctx, cancel := context.WithCancel(context.Background())
	job.cancel = cancel

	job.Cancel()

	select {
	case <-ctx.Done():
		// expected: context was cancelled
	default:
		t.Fatal("expected context to be done after Cancel()")
	}
}

func TestJobCancel_NilCancelFunc(t *testing.T) {
	job := NewJob(0, 1, "DISC", "/output")
	// cancel is nil by default; calling Cancel() should not panic
	job.Cancel()
}

func TestJobSnapshot(t *testing.T) {
	job := NewJob(0, 1, "DISC_NAME", "/output")
	job.ID = 42
	job.TitleName = "The Matrix"
	job.ContentType = "Movie"
	job.TrackMetadata = TrackMetadata{
		SizeBytes: 1024,
		SizeHuman: "1 KB",
		Duration:  "1:30:00",
		AudioTracks: []AudioTrack{
			{Language: "English", Codec: "TrueHD", Channels: "7.1"},
		},
		SubtitleLanguages: []string{"English", "French"},
	}

	job.Start()
	job.UpdateProgress(50)

	snap := job.Snapshot()

	// Verify all fields match.
	if snap.ID != 42 {
		t.Errorf("ID = %d, want 42", snap.ID)
	}
	if snap.DriveIndex != 0 {
		t.Errorf("DriveIndex = %d, want 0", snap.DriveIndex)
	}
	if snap.TitleIndex != 1 {
		t.Errorf("TitleIndex = %d, want 1", snap.TitleIndex)
	}
	if snap.DiscName != "DISC_NAME" {
		t.Errorf("DiscName = %q, want %q", snap.DiscName, "DISC_NAME")
	}
	if snap.TitleName != "The Matrix" {
		t.Errorf("TitleName = %q, want %q", snap.TitleName, "The Matrix")
	}
	if snap.ContentType != "Movie" {
		t.Errorf("ContentType = %q, want %q", snap.ContentType, "Movie")
	}
	if snap.Status != StatusRipping {
		t.Errorf("Status = %q, want %q", snap.Status, StatusRipping)
	}
	if snap.Progress != 50 {
		t.Errorf("Progress = %d, want 50", snap.Progress)
	}
	if snap.StartedAt.IsZero() {
		t.Error("StartedAt should be set after Start()")
	}

	// Modify the original and verify the snapshot is independent.
	job.UpdateProgress(75)
	if snap.Progress != 50 {
		t.Errorf("snapshot Progress changed to %d after original update; want 50 (independent copy)", snap.Progress)
	}
}

func TestJobSnapshot_CopiesTrackMetadata(t *testing.T) {
	job := NewJob(0, 1, "DISC", "/output")
	job.TrackMetadata = TrackMetadata{
		SizeBytes: 50000000000,
		SizeHuman: "50 GB",
		Duration:  "2:15:00",
		AudioTracks: []AudioTrack{
			{Language: "English", Codec: "DTS-HD MA", Channels: "7.1"},
			{Language: "Spanish", Codec: "AC3", Channels: "5.1"},
		},
		SubtitleLanguages: []string{"English", "French", "Spanish"},
	}

	snap := job.Snapshot()

	// Verify TrackMetadata scalar fields.
	if snap.TrackMetadata.SizeBytes != 50000000000 {
		t.Errorf("SizeBytes = %d, want 50000000000", snap.TrackMetadata.SizeBytes)
	}
	if snap.TrackMetadata.SizeHuman != "50 GB" {
		t.Errorf("SizeHuman = %q, want %q", snap.TrackMetadata.SizeHuman, "50 GB")
	}
	if snap.TrackMetadata.Duration != "2:15:00" {
		t.Errorf("Duration = %q, want %q", snap.TrackMetadata.Duration, "2:15:00")
	}

	// Verify AudioTracks.
	if len(snap.TrackMetadata.AudioTracks) != 2 {
		t.Fatalf("AudioTracks len = %d, want 2", len(snap.TrackMetadata.AudioTracks))
	}
	if snap.TrackMetadata.AudioTracks[0].Language != "English" {
		t.Errorf("AudioTracks[0].Language = %q, want %q", snap.TrackMetadata.AudioTracks[0].Language, "English")
	}
	if snap.TrackMetadata.AudioTracks[0].Codec != "DTS-HD MA" {
		t.Errorf("AudioTracks[0].Codec = %q, want %q", snap.TrackMetadata.AudioTracks[0].Codec, "DTS-HD MA")
	}
	if snap.TrackMetadata.AudioTracks[1].Language != "Spanish" {
		t.Errorf("AudioTracks[1].Language = %q, want %q", snap.TrackMetadata.AudioTracks[1].Language, "Spanish")
	}

	// Verify SubtitleLanguages.
	if len(snap.TrackMetadata.SubtitleLanguages) != 3 {
		t.Fatalf("SubtitleLanguages len = %d, want 3", len(snap.TrackMetadata.SubtitleLanguages))
	}
	if snap.TrackMetadata.SubtitleLanguages[0] != "English" {
		t.Errorf("SubtitleLanguages[0] = %q, want %q", snap.TrackMetadata.SubtitleLanguages[0], "English")
	}
	if snap.TrackMetadata.SubtitleLanguages[2] != "Spanish" {
		t.Errorf("SubtitleLanguages[2] = %q, want %q", snap.TrackMetadata.SubtitleLanguages[2], "Spanish")
	}
}
