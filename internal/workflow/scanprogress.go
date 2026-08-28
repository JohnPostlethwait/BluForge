package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// ErrScanInProgress reports that a drive is already being scanned. It is not a
// failure: the page is already watching the scan that is running.
var ErrScanInProgress = errors.New("a scan is already running")

// ProgressScanner is a scanner that narrates itself. Optional: a scanner
// without it still works, it just has nothing to say while it runs.
type ProgressScanner interface {
	ScanDiscWithProgress(ctx context.Context, driveIndex int, onEvent func(makemkv.Event)) (*makemkv.DiscScan, error)
}

// ScanStatus is what the page needs to render the scan banner, including a page
// that just reconnected and missed the events so far.
type ScanStatus struct {
	Active    bool      `json:"active"`
	Operation string    `json:"operation"`
	StartedAt time.Time `json:"startedAt"`
}

// scanState tracks one in-flight scan.
type scanState struct {
	startedAt time.Time
	operation string
	// lastBroadcast throttles narration. A damaged disc emits a message per
	// retry, and the SSE hub drops events once a client's buffer fills — which
	// would cost the "done" event that takes the banner down.
	lastBroadcast time.Time
}

// scanNarrationInterval bounds how often an unchanged scan re-announces itself.
const scanNarrationInterval = time.Second

// StartScan runs a scan in the background, returning as soon as it is under
// way.
//
// A scan of a disc that retries unreadable sectors runs for the better part of
// an hour. Held open on the request it outlived the browser, and the browser
// giving up killed makemkvcon mid-read — which is what "signal: killed" was.
func (o *Orchestrator) StartScan(driveIndex int) error {
	return o.startScan(driveIndex, false)
}

// StartRescan reads the disc in the drive whatever is cached for it, reporting
// progress the same way StartScan does.
//
// This is what the Scan button runs. A cached scan cannot tell that the disc
// was swapped for another one answering to the same volume label, and finding
// out means reading the disc.
func (o *Orchestrator) StartRescan(driveIndex int) error {
	return o.startScan(driveIndex, true)
}

func (o *Orchestrator) startScan(driveIndex int, force bool) error {
	if o.scanner == nil {
		return errors.New("no scanner configured")
	}
	if !o.beginScan(driveIndex) {
		return ErrScanInProgress
	}

	go func() {
		// A safety net only. The slot is released explicitly below, before the
		// outcome is announced; this catches the case where scanDisc panics.
		defer o.endScan(driveIndex)

		// Detached on purpose: nothing about this scan belongs to a request.
		scan, err := o.scanDisc(context.Background(), driveIndex, force)

		// Release the slot before saying how the scan ended.
		//
		// The page reacts to "done" by fetching the titles and resyncing the
		// drive state, and that resync asks ScanStatus. Announcing first left a
		// window where it answered "still scanning", which puts the banner back
		// up over a scan that has finished — and nothing takes it down again,
		// because the event that would have is the one already spent.
		o.endScan(driveIndex)

		switch {
		case errors.Is(err, ErrRecoveryInProgress):
			// Recovery took over and broadcasts its own progress. Ending the
			// scan banner here hands the page to the recovery banner.
			o.broadcastScan(driveIndex, "recovering", "")
		case err != nil:
			slog.Error("scan: failed", "drive_index", driveIndex, "error", err)
			o.broadcastScan(driveIndex, "failed", err.Error())
		default:
			slog.Info("scan: complete", "drive_index", driveIndex,
				"disc", scan.DiscName, "titles", len(scan.Titles))
			o.broadcastScan(driveIndex, "done", "")
		}
	}()

	return nil
}

// scanProgressFn returns the callback that turns makemkvcon's output into
// something the page can show.
func (o *Orchestrator) scanProgressFn(driveIndex int) func(makemkv.Event) {
	return func(ev makemkv.Event) {
		// PRGT/PRGC name the step. A message is the fallback: on a damaged disc
		// the read errors are the only sign of life for minutes at a time.
		operation := ev.Operation
		if operation == "" && ev.Message != nil {
			operation = ev.Message.Text
		}
		if operation == "" {
			return
		}

		o.scanMu.Lock()
		st := o.scanning[driveIndex]
		if st == nil {
			o.scanMu.Unlock()
			return
		}
		due := st.operation != operation || time.Since(st.lastBroadcast) >= scanNarrationInterval
		st.operation = operation
		if due {
			st.lastBroadcast = time.Now()
		}
		payload := scanPayload(driveIndex, "scanning", operation, "", st.startedAt)
		o.scanMu.Unlock()

		if due {
			o.emit(payload)
		}
	}
}

// beginScan claims the scan slot for a drive, returning false when one is
// already running. A scan that looks stuck invites another click, and a second
// makemkvcon would only queue behind the first.
func (o *Orchestrator) beginScan(driveIndex int) bool {
	o.scanMu.Lock()
	if o.scanning == nil {
		o.scanning = make(map[int]*scanState)
	}
	if o.scanning[driveIndex] != nil {
		o.scanMu.Unlock()
		return false
	}
	st := &scanState{startedAt: time.Now(), operation: "Starting"}
	o.scanning[driveIndex] = st
	payload := scanPayload(driveIndex, "scanning", st.operation, "", st.startedAt)
	o.scanMu.Unlock()

	o.emit(payload)
	return true
}

// endScan clears the slot. It runs before the terminal event is broadcast, so a
// client that acts on "done" immediately does not find a scan still running.
func (o *Orchestrator) endScan(driveIndex int) {
	o.scanMu.Lock()
	delete(o.scanning, driveIndex)
	o.scanMu.Unlock()
}

// ScanStatus reports the in-flight scan for a drive.
//
// Exposed because a client that lost its event stream cannot tell: events are
// delivered once and never replayed, so the page has to be able to ask rather
// than wait for one that already happened.
func (o *Orchestrator) ScanStatus(driveIndex int) ScanStatus {
	o.scanMu.Lock()
	defer o.scanMu.Unlock()
	st := o.scanning[driveIndex]
	if st == nil {
		return ScanStatus{}
	}
	return ScanStatus{Active: true, Operation: st.operation, StartedAt: st.startedAt}
}

// broadcastScan pushes a terminal scan phase to the page.
func (o *Orchestrator) broadcastScan(driveIndex int, phase, message string) {
	o.scanMu.Lock()
	var startedAt time.Time
	var operation string
	if st := o.scanning[driveIndex]; st != nil {
		startedAt, operation = st.startedAt, st.operation
	}
	payload := scanPayload(driveIndex, phase, operation, message, startedAt)
	o.scanMu.Unlock()

	o.emit(payload)
}

// scanPayload builds the SSE body. The start time goes with it so the client
// can tick the elapsed count itself rather than needing a heartbeat.
func scanPayload(driveIndex int, phase, operation, message string, startedAt time.Time) map[string]any {
	var started int64
	if !startedAt.IsZero() {
		started = startedAt.Unix()
	}
	return map[string]any{
		"drive_index": driveIndex,
		"phase":       phase,
		"operation":   operation,
		"message":     message,
		"started_at":  started,
	}
}

// emit sends a scan payload. Marshalling happens outside the lock so a slow
// subscriber cannot stall a scan.
func (o *Orchestrator) emit(payload map[string]any) {
	if o.onBroadcast == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("scan: could not marshal SSE payload", "error", err)
		return
	}
	o.onBroadcast("disc_scan", string(data))
}

// scanOnce runs the scanner, narrating it when the scanner supports it.
func (o *Orchestrator) scanOnce(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	if ps, ok := o.scanner.(ProgressScanner); ok {
		return ps.ScanDiscWithProgress(ctx, driveIndex, o.scanProgressFn(driveIndex))
	}
	return o.scanner.ScanDisc(ctx, driveIndex)
}
