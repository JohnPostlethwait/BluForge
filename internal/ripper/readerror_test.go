package ripper

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// makemkvcon retried a bad patch on The Ten Commandments for two hours without
// failing the rip, and the page showed it copying at 23.7 MB/s. Its read
// errors are the only sign; the job has to carry them to the page, and a burst
// of hundreds must not flood the page's event stream.
func TestARipCarriesItsLatestReadErrorToThePage(t *testing.T) {
	exec := &phasedExecutor{done: make(chan struct{})}
	exec.run = func(onEvent func(makemkv.Event)) {
		onEvent(makemkv.Event{Type: "MSG", Message: &makemkv.Message{
			Code: makemkv.MsgSavingTitles, Text: "Saving 1 titles into directory /out",
		}})
		for i := 0; i < 200; i++ {
			onEvent(makemkv.Event{Type: "MSG", Message: &makemkv.Message{
				Code: makemkv.MsgHashCheckFailed,
				Text: fmt.Sprintf("Hash check failed for file 00294.m2ts at offset %d, file is corrupt.", 32788899840+i),
			}})
		}
		onEvent(makemkv.Event{Type: "MSG", Message: &makemkv.Message{
			Code: makemkv.MsgTooManyHashErrors, Text: "Too many hash check errors in file 00294.m2ts.",
		}})
	}

	var mu sync.Mutex
	var withError int
	var atOrganize Job
	settled := make(chan struct{})
	job := NewJob(0, 0, "DISC", t.TempDir())
	job.OnComplete = func(j *Job, _ error) error {
		atOrganize = j.Snapshot()
		close(settled)
		return nil
	}

	engine := NewEngine(exec)
	engine.OnUpdate(func(j *Job) {
		if j.Snapshot().ReadError != "" {
			mu.Lock()
			withError++
			mu.Unlock()
		}
	})
	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-settled:
	case <-time.After(30 * time.Second):
		t.Fatal("the rip never finished")
	}

	if !strings.Contains(atOrganize.ReadError, "Too many hash check errors") {
		t.Errorf("ReadError = %q, want the latest read error", atOrganize.ReadError)
	}
	if atOrganize.ReadErrorAt.IsZero() {
		t.Error("ReadErrorAt not recorded")
	}
	mu.Lock()
	defer mu.Unlock()
	if withError == 0 {
		t.Error("no update told the page about the read errors")
	}
	if withError > 3 {
		t.Errorf("201 read errors in a burst sent %d updates, want a few", withError)
	}
}

func TestReadErrorCodes(t *testing.T) {
	for _, code := range []int{makemkv.MsgSourceCorrupt, makemkv.MsgAVSyncIssues, makemkv.MsgHashCheckFailed, makemkv.MsgTooManyHashErrors} {
		if !makemkv.IsReadError(code) {
			t.Errorf("IsReadError(%d) = false", code)
		}
	}
	if makemkv.IsReadError(makemkv.MsgSavingTitles) {
		t.Error("the copy starting is not a read error")
	}
}
