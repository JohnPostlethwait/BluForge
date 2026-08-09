package makemkv

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// recordingRunner captures every argv it is asked to run so tests can assert on
// the exact command line, and can return different output per call.
type recordingRunner struct {
	mu     sync.Mutex
	calls  [][]string
	output string
	err    error
}

func (r *recordingRunner) Run(_ context.Context, args ...string) (*strings.Reader, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string(nil), args...))
	r.mu.Unlock()
	return strings.NewReader(r.output), r.err
}

func (r *recordingRunner) lastCall() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return nil
	}
	return r.calls[len(r.calls)-1]
}

func argvContains(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

// The backup must NOT pass --decrypt. Decryption is exactly what fails on these
// discs; the whole point is to take a raw copy and remove AACS afterwards.
func TestBackupOmitsDecryptFlag(t *testing.T) {
	runner := &recordingRunner{output: "PRGV:100,100,100\n"}
	ex := NewExecutor(WithRunner(runner))

	if err := ex.Backup(context.Background(), 1, "/scratch/slug", nil); err != nil {
		t.Fatalf("Backup returned error: %v", err)
	}

	argv := runner.lastCall()
	if argv == nil {
		t.Fatal("Backup did not invoke the runner")
	}
	if argvContains(argv, "--decrypt") {
		t.Errorf("Backup passed --decrypt; argv = %v", argv)
	}
	if !argvContains(argv, "backup") {
		t.Errorf("Backup did not issue the backup verb; argv = %v", argv)
	}
	if !argvContains(argv, "disc:1") {
		t.Errorf("Backup did not target disc:1; argv = %v", argv)
	}
	if !argvContains(argv, "/scratch/slug") {
		t.Errorf("Backup did not pass the destination; argv = %v", argv)
	}
}

func TestBackupReportsProgress(t *testing.T) {
	runner := &recordingRunner{output: "PRGV:50,5000,10000\nPRGV:100,10000,10000\n"}
	ex := NewExecutor(WithRunner(runner))

	var got []int
	err := ex.Backup(context.Background(), 0, "/scratch/slug", func(ev Event) {
		if ev.Type == "PRGV" && ev.Progress != nil && ev.Progress.Max > 0 {
			got = append(got, ev.Progress.Total*100/ev.Progress.Max)
		}
	})
	if err != nil {
		t.Fatalf("Backup returned error: %v", err)
	}

	if len(got) != 2 || got[0] != 50 || got[1] != 100 {
		t.Errorf("progress percentages = %v, want [50 100]", got)
	}
}

// A backup that fails because the container cannot see any optical drive must
// say so, rather than surfacing as a bare "backup failed" that sends the user
// looking in the wrong place.
func TestBackupNoDrivesGivesActionableError(t *testing.T) {
	runner := &recordingRunner{
		output: `MSG:5042,0,0,"The program can't find any usable optical drives.","%1","The program can't find any usable optical drives."`,
		err:    errors.New("exit status 1"),
	}
	ex := NewExecutor(WithRunner(runner))

	err := ex.Backup(context.Background(), 0, "/scratch/slug", nil)
	if err == nil {
		t.Fatal("Backup succeeded, want error")
	}
	if !errors.Is(err, ErrNoOpticalDrives) {
		t.Errorf("error = %v, want it to wrap ErrNoOpticalDrives", err)
	}
}

// ScanSource is what lets a stripped backup folder stand in for the disc.
func TestScanSourceUsesFileTarget(t *testing.T) {
	runner := &recordingRunner{output: scanDiscOutput}
	ex := NewExecutor(WithRunner(runner))

	scan, err := ex.ScanSource(context.Background(), FileSource("/scratch/slug"))
	if err != nil {
		t.Fatalf("ScanSource returned error: %v", err)
	}
	if !argvContains(runner.lastCall(), "file:/scratch/slug") {
		t.Errorf("ScanSource did not target the file source; argv = %v", runner.lastCall())
	}
	if len(scan.Titles) == 0 {
		t.Error("ScanSource returned no titles")
	}
}

// A failed scan must carry its messages out, or the caller cannot tell a
// spurious-AACS failure from any other open failure.
func TestScanSourceFailureReturnsScanError(t *testing.T) {
	const spuriousOutput = `MSG:3303,0,0,"The volume key is unknown for this disc - video can't be decrypted","%1","The volume key is unknown for this disc - video can't be decrypted"
MSG:5010,0,0,"Failed to open disc","%1","Failed to open disc"
TCOUT:0
`
	runner := &recordingRunner{output: spuriousOutput, err: errors.New("exit status 1")}
	ex := NewExecutor(WithRunner(runner))

	_, err := ex.ScanSource(context.Background(), DiscSource(2))
	if err == nil {
		t.Fatal("ScanSource succeeded, want error")
	}

	var se *ScanError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *ScanError", err)
	}
	if !IsSpuriousAACSSignature(se.Messages(), len(se.Scan.Titles)) {
		t.Errorf("signature not detectable from ScanError; messages = %v", MessageCodes(se.Messages()))
	}
}

// MSG:5042 is emitted on nearly every invocation including successful ones, so
// it must never on its own turn a working file-source scan into an error.
func TestScanSourceIgnores5042OnFileSource(t *testing.T) {
	runner := &recordingRunner{
		output: `MSG:5042,0,0,"The program can't find any usable optical drives.","%1","The program can't find any usable optical drives."` + "\n" + scanDiscOutput,
	}
	ex := NewExecutor(WithRunner(runner))

	scan, err := ex.ScanSource(context.Background(), FileSource("/scratch/slug"))
	if err != nil {
		t.Fatalf("ScanSource returned error on benign 5042: %v", err)
	}
	if len(scan.Titles) == 0 {
		t.Error("ScanSource returned no titles")
	}
}
