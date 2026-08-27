package web

import (
	"context"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
)

// The track picker's language list is rebuilt in the browser every time a scan
// finishes, and the browser only ever knew the languages a *cached* scan had
// already produced. On the ordinary path — insert disc, scan, pick tracks —
// there is no cached scan at page load, so the client had nothing to prefer and
// pre-selected every audio and subtitle language on the disc. The configured
// preferences have to reach the page whether or not a scan exists yet.
func TestDriveStoreCarriesPreferredLanguagesWithoutCachedScan(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "SOME_DISC"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	srv.cfg = &config.AppConfig{
		OutputDir:              "/tmp/test",
		PreferredAudioLangs:    "eng,jpn",
		PreferredSubtitleLangs: "eng",
	}

	drv := mgr.GetDrive(0)
	if drv == nil {
		t.Fatal("drive 0 not found")
	}
	store := srv.buildDriveStore(0, drv)

	if got, want := store.PreferredAudioLangs, []string{"eng", "jpn"}; !equalStrings(got, want) {
		t.Errorf("PreferredAudioLangs = %v, want %v", got, want)
	}
	if got, want := store.PreferredSubtitleLangs, []string{"eng"}; !equalStrings(got, want) {
		t.Errorf("PreferredSubtitleLangs = %v, want %v", got, want)
	}
}

// An empty preference means "no opinion", and the page has to be able to tell
// that apart from a preference it simply was not given: the first selects
// everything, the second would select nothing.
func TestDriveStorePreferredLanguagesEmptyWhenUnset(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "SOME_DISC"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)

	drv := mgr.GetDrive(0)
	if drv == nil {
		t.Fatal("drive 0 not found")
	}
	store := srv.buildDriveStore(0, drv)

	if store.PreferredAudioLangs == nil {
		t.Error("PreferredAudioLangs = nil, want an empty slice so it marshals as []")
	}
	if len(store.PreferredAudioLangs) != 0 {
		t.Errorf("PreferredAudioLangs = %v, want empty", store.PreferredAudioLangs)
	}
	if store.PreferredSubtitleLangs == nil {
		t.Error("PreferredSubtitleLangs = nil, want an empty slice so it marshals as []")
	}
	if len(store.PreferredSubtitleLangs) != 0 {
		t.Errorf("PreferredSubtitleLangs = %v, want empty", store.PreferredSubtitleLangs)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
