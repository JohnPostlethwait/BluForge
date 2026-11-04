package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/templates"
)

const (
	asusName = "BD-RE ASUS BW-16D1HT 3.10 KLFO82D3028"
	lgName   = "BD-RE HL-DT-ST BD-RE  WH16NS40 1.02 SIK9TH8A1234"
)

func TestIgnoredDriveOptions_ListsAttachedAndDetachedIgnoredDrives(t *testing.T) {
	listed := []makemkv.DriveInfo{
		{Index: 0, DriveName: lgName, DevicePath: "/dev/sr0"},
		{Index: 1, DriveName: asusName, DevicePath: "/dev/sr1"},
		{Index: 2, DriveName: "", DevicePath: "/dev/sr2"}, // nameless: cannot be ignored
	}
	ignored := []string{asusName, "BD-RE PIONEER BDR-XD08U 1.01 ZZZ999"}

	got := ignoredDriveOptions(listed, ignored)
	want := []templates.IgnoredDriveOption{
		{Name: lgName, DevicePath: "/dev/sr0", Ignored: false},
		{Name: asusName, DevicePath: "/dev/sr1", Ignored: true},
		{Name: "BD-RE PIONEER BDR-XD08U 1.01 ZZZ999", DevicePath: "", Ignored: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

// A stored name with an old firmware revision is the same drive as the one
// attached now; it is listed once, under the name the drive reports today.
func TestIgnoredDriveOptions_MatchesAcrossFirmwareRevisions(t *testing.T) {
	listed := []makemkv.DriveInfo{{Index: 1, DriveName: asusName, DevicePath: "/dev/sr1"}}
	ignored := []string{"BD-RE ASUS BW-16D1HT 3.02 KLFO82D3028"}

	got := ignoredDriveOptions(listed, ignored)
	want := []templates.IgnoredDriveOption{{Name: asusName, DevicePath: "/dev/sr1", Ignored: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestHandleSettingsSave_IgnoredDrives(t *testing.T) {
	srv := testSettingsServer(t)

	form := url.Values{}
	form.Set("output_dir", "/new/output")
	form.Set("duplicate_action", "skip")
	form.Add("ignored_drives", asusName)
	form.Add("ignored_drives", "BD-RE ASUS BW-16D1HT 3.02 KLFO82D3028") // same drive
	form.Add("ignored_drives", "  ")

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := srv.GetConfig().IgnoredDrives; !reflect.DeepEqual(got, []string{asusName}) {
		t.Errorf("IgnoredDrives = %q, want [%q]", got, asusName)
	}

	// Unchecking every box clears the list.
	form.Del("ignored_drives")
	req = httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)
	if got := srv.GetConfig().IgnoredDrives; len(got) != 0 {
		t.Errorf("IgnoredDrives = %q, want none", got)
	}
}

// Saving the setting reaches the drive manager, so the next poll applies it
// without a restart.
func TestHandleSettingsSave_IgnoredDrivesTakeEffectOnNextPoll(t *testing.T) {
	srv := testSettingsServer(t)
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "DISC"}, nil)
	srv.driveMgr = mgr
	mgr.PollOnce(context.Background())
	if len(mgr.GetAllDrives()) != 1 {
		t.Fatal("the drive should be tracked before it is ignored")
	}

	form := url.Values{}
	form.Set("output_dir", "/new/output")
	form.Set("duplicate_action", "skip")
	form.Add("ignored_drives", "Test Drive")
	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.echo.ServeHTTP(httptest.NewRecorder(), req)

	mgr.PollOnce(context.Background())
	if n := len(mgr.GetAllDrives()); n != 0 {
		t.Errorf("drives after ignoring = %d, want 0", n)
	}
}
