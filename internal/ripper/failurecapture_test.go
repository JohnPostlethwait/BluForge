package ripper

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// failingRipExecutor narrates an enumeration and then fails, the way the rip of
// Kiki's Delivery Service did.
type failingRipExecutor struct{}

func (m *failingRipExecutor) StartRip(_ context.Context, _ makemkv.Source, _ int, _ string, _ string, onEvent func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	if onEvent != nil {
		onEvent(makemkv.Event{Type: "MSG", Message: &makemkv.Message{
			Code: 3307, Text: "File 00303.mpls was added as title #0",
		}})
		onEvent(makemkv.Event{Type: "MSG", Message: &makemkv.Message{
			Code: 3308, Text: "File 00200.mpls (angle 1) was added as title #3",
		}})
		onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Current: 1, Total: 1, Max: 1}})
	}
	return errors.New("makemkv: rip disc:1 title 3: the title was not in this pass")
}

// levelCapture keeps records with their levels, so a test can ask what level a
// line was written at rather than only whether it appeared.
type levelCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *levelCapture) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelInfo }

func (c *levelCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}

func (c *levelCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *levelCapture) WithGroup(string) slog.Handler      { return c }

// errorText renders every ERROR record, attributes included.
func (c *levelCapture) errorText() string {
	return c.render(slog.LevelError, "")
}

// findAt renders the first record at level whose message contains substr, or
// "" when there is none.
func (c *levelCapture) findAt(level slog.Level, substr string) string {
	return c.render(level, substr)
}

func (c *levelCapture) render(level slog.Level, substr string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, r := range c.records {
		if r.Level != level || !strings.Contains(r.Message, substr) {
			continue
		}
		b.WriteString(r.Message)
		r.Attrs(func(a slog.Attr) bool {
			b.WriteString(" ")
			b.WriteString(a.Key)
			b.WriteString("=")
			b.WriteString(a.Value.String())
			return true
		})
		b.WriteString("\n")
		if substr != "" {
			return b.String()
		}
	}
	return b.String()
}

// runFailingJob submits one job that fails and waits for it to settle.
func runFailingJob(t *testing.T) (*Job, *levelCapture) {
	t.Helper()

	cap := &levelCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(cap))
	defer slog.SetDefault(prev)

	engine := NewEngine(&failingRipExecutor{})
	job := NewJob(0, 3, "KIKIS_DELIVERY_SERVICE_BD", "/output")
	job.SourceFile = "00200.mpls"
	if err := engine.Submit(job); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if job.Snapshot().Status == StatusFailed {
			return job, cap
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the job never reached a failed state")
	return nil, nil
}

// A failed rip used to persist one line of error and nothing else. Everything
// MakeMKV said on the way down was parsed, logged, and dropped — so explaining
// the failure meant finding the container's log and knowing the timestamp.
func TestAFailedRipKeepsWhatMakeMKVSaid(t *testing.T) {
	job, _ := runFailingJob(t)

	captured := job.Snapshot().FailureOutput
	if len(captured) == 0 {
		t.Fatal("the failed job kept none of the rip's messages")
	}

	var texts []string
	for _, m := range captured {
		texts = append(texts, m.Text)
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{"00303.mpls", "00200.mpls (angle 1)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the capture is missing %q:\n%s", want, joined)
		}
	}
}

// Progress is the bulk of the stream and explains nothing. It must not crowd
// out the lines that do.
func TestAFailedRipDoesNotKeepProgressEvents(t *testing.T) {
	job, _ := runFailingJob(t)

	if got := len(job.Snapshot().FailureOutput); got != 2 {
		t.Errorf("kept %d entries, want the 2 messages and no progress: %+v",
			got, job.Snapshot().FailureOutput)
	}
}

// The detail rides on the error, not on the level. Turning the log level up
// afterwards cannot recover what a failure did not report at the time, and a
// rip costs a disc and half an hour to repeat.
// The failure is one actionable ERROR line: what failed, on which disc and
// title, and why. It does NOT dump the whole captured message list into the log
// — that was a wall of text nobody could read. The captured detail lives on the
// job for the activity page; the log stays legible.
func TestTheFailureIsOneActionableLineNotADump(t *testing.T) {
	job, cap := runFailingJob(t)

	out := cap.errorText()
	if out == "" {
		t.Fatal("the rip failed without an ERROR record")
	}
	// Actionable context is present.
	for _, want := range []string{"KIKIS_DELIVERY_SERVICE_BD", "00200.mpls"} {
		if !strings.Contains(out, want) {
			t.Errorf("the failure line is missing context %q:\n%s", want, out)
		}
	}
	// The enumeration blob is NOT dumped into the log line.
	if strings.Contains(out, "was added as title") {
		t.Errorf("the captured message blob was dumped into the ERROR line:\n%s", out)
	}
	// But it is still on the job, for the UI to show.
	if len(job.Snapshot().FailureOutput) == 0 {
		t.Error("the captured detail was lost instead of kept on the job")
	}
}
