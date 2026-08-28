package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// gatedBackupper blocks inside Backup until released, and records the context
// it was handed so a test can prove the backup does not die with the caller's.
type gatedBackupper struct {
	fakeBackupper

	started  chan struct{}
	release  chan struct{}
	ctxSeen  context.Context
	ctxMu    sync.Mutex
	attempts int
}

func newGatedBackupper(discRoot string) *gatedBackupper {
	return &gatedBackupper{
		fakeBackupper: fakeBackupper{discRoot: discRoot},
		started:       make(chan struct{}, 4),
		release:       make(chan struct{}),
	}
}

func (g *gatedBackupper) Backup(ctx context.Context, driveIndex int, destDir string, onEvent func(makemkv.Event)) error {
	g.ctxMu.Lock()
	g.ctxSeen = ctx
	g.attempts++
	g.ctxMu.Unlock()

	g.started <- struct{}{}
	<-g.release
	return g.fakeBackupper.Backup(ctx, driveIndex, destDir, onEvent)
}

func (g *gatedBackupper) backupContext() context.Context {
	g.ctxMu.Lock()
	defer g.ctxMu.Unlock()
	return g.ctxSeen
}

func (g *gatedBackupper) backupAttempts() int {
	g.ctxMu.Lock()
	defer g.ctxMu.Unlock()
	return g.attempts
}

func setupAsync(t *testing.T) (*Orchestrator, *gatedBackupper, string) {
	t.Helper()
	return setupAsyncWithBroadcast(t, func(string, string) {})
}

// setupAsyncWithBroadcast is setupAsync with the SSE callback under the test's
// control, for asserting on what recovery announces and when it says it.
func setupAsyncWithBroadcast(t *testing.T, broadcast func(string, string)) (*Orchestrator, *gatedBackupper, string) {
	t.Helper()

	tmp := t.TempDir()
	store, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	output := filepath.Join(tmp, "output")
	if err := os.MkdirAll(output, 0o777); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}

	discRoot := writeFakeDisc(t, false)
	backupper := newGatedBackupper(discRoot)

	orch := NewOrchestrator(OrchestratorDeps{
		Store:       store,
		Engine:      ripper.NewEngine(&mockRipExecutor{}),
		Organizer:   organizer.New(),
		OnBroadcast: broadcast,
		Scanner:     &failingScanner{devicePath: "/dev/sr0"},
		Backupper:   backupper,
		OpenDiscRoot: func(string) (string, func(), error) {
			return discRoot, func() {}, nil
		},
	})
	orch.SetOutputDir(output)

	// Recovery is detached from the caller by design, so it can still be writing
	// into the scratch directory when the test returns. Cleanups run LIFO, so
	// this one drains it before t.TempDir removes the tree underneath it.
	t.Cleanup(func() {
		deadline := time.Now().Add(asyncDeadline)
		for time.Now().Before(deadline) {
			if !orch.recoveryRunning(0) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Log("recovery still running at cleanup; scratch teardown may race")
	})

	return orch, backupper, output
}

// A disc backup takes tens of minutes. Running it inside the caller's context
// meant it inherited the lifetime of an HTTP request: when the browser gave up,
// exec.CommandContext sent SIGKILL and the backup died with "signal: killed".
// ScanDisc must hand recovery off and return.
func TestScanDiscDoesNotBlockOnRecovery(t *testing.T) {
	orch, backupper, _ := setupAsync(t)
	defer close(backupper.release)

	done := make(chan error, 1)
	go func() {
		_, err := orch.ScanDisc(context.Background(), 0)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrRecoveryInProgress) {
			t.Fatalf("ScanDisc returned %v, want ErrRecoveryInProgress", err)
		}
	case <-time.After(asyncDeadline):
		t.Fatal("ScanDisc blocked waiting for the backup instead of handing it off")
	}

	select {
	case <-backupper.started:
	case <-time.After(asyncDeadline):
		t.Fatal("recovery did not start in the background")
	}
}

// The backup must outlive the request that triggered it. Cancelling the
// caller's context is exactly what killed it in production.
func TestRecoveryBackupSurvivesCallerCancellation(t *testing.T) {
	orch, backupper, _ := setupAsync(t)
	defer close(backupper.release)

	ctx, cancel := context.WithCancel(context.Background())
	if _, err := orch.ScanDisc(ctx, 0); !errors.Is(err, ErrRecoveryInProgress) {
		t.Fatalf("ScanDisc returned %v, want ErrRecoveryInProgress", err)
	}

	select {
	case <-backupper.started:
	case <-time.After(asyncDeadline):
		t.Fatal("recovery did not start")
	}

	// The request goes away, as it does when a browser stops waiting.
	cancel()

	backupCtx := backupper.backupContext()
	if backupCtx == nil {
		t.Fatal("no context captured")
	}
	select {
	case <-backupCtx.Done():
		t.Fatalf("backup context was cancelled with the caller: %v", backupCtx.Err())
	case <-time.After(100 * time.Millisecond):
		// Still alive, which is the point.
	}
}

// Scanning again while a backup is running must not start a second one — that
// would mean two ~100GB copies of the same disc racing each other.
func TestConcurrentScansStartOneRecovery(t *testing.T) {
	orch, backupper, _ := setupAsync(t)
	defer close(backupper.release)

	for i := 0; i < 3; i++ {
		if _, err := orch.ScanDisc(context.Background(), 0); !errors.Is(err, ErrRecoveryInProgress) {
			t.Fatalf("scan %d returned %v, want ErrRecoveryInProgress", i, err)
		}
	}

	select {
	case <-backupper.started:
	case <-time.After(asyncDeadline):
		t.Fatal("recovery did not start")
	}
	time.Sleep(100 * time.Millisecond)

	if n := backupper.backupAttempts(); n != 1 {
		t.Errorf("started %d backups for one disc, want 1", n)
	}
}

// Once recovery finishes, the disc behaves like any other: a scan returns its
// titles, with no second backup.
func TestScanAfterRecoveryReturnsTitles(t *testing.T) {
	orch, backupper, _ := setupAsync(t)

	if _, err := orch.ScanDisc(context.Background(), 0); !errors.Is(err, ErrRecoveryInProgress) {
		t.Fatalf("ScanDisc returned %v, want ErrRecoveryInProgress", err)
	}
	<-backupper.started
	close(backupper.release)

	var scan *makemkv.DiscScan
	deadline := time.Now().Add(asyncDeadline)
	for time.Now().Before(deadline) {
		s, err := orch.ScanDisc(context.Background(), 0)
		if err == nil {
			scan = s
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if scan == nil {
		t.Fatal("scan never succeeded after recovery completed")
	}
	if len(scan.Titles) == 0 {
		t.Error("recovered scan has no titles")
	}
	if n := backupper.backupAttempts(); n != 1 {
		t.Errorf("ran %d backups, want 1", n)
	}
}
