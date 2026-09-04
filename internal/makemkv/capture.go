package makemkv

import "sync"

// DefaultCaptureLimit is how many distinct messages one operation keeps.
//
// The failing Kiki's Delivery Service rip produced about twenty-five. This is a
// safety valve against a disc nobody has met yet, not an expected ceiling —
// repeats do not count against it, so the only way to reach it is a disc that
// says two hundred different things.
const DefaultCaptureLimit = 200

// MessageCapture keeps an operation's MakeMKV messages so a failure can be
// reported with the account of what led to it.
//
// A rip's messages were parsed, logged, and dropped. When one failed, the
// explanation existed only in the container's log — findable if you knew the
// timestamp, gone at the next restart, and never beside the job it belonged to.
//
// Repeats collapse into a count rather than accumulating. A disc with damaged
// sectors emits the same read error once per retry, for thousands of retries: a
// rip running half an hour would otherwise grow this without limit, and bury
// the few lines that say what happened under copies of one line that does not.
// This is what ScanOutput already does with a scan's messages, and it produces
// the same type so the two render the same way.
type MessageCapture struct {
	mu      sync.Mutex
	limit   int
	index   map[string]int
	out     []ScanWarning
	dropped int
}

// NewMessageCapture returns a capture holding at most limit distinct messages.
func NewMessageCapture(limit int) *MessageCapture {
	if limit <= 0 {
		limit = DefaultCaptureLimit
	}
	return &MessageCapture{limit: limit, index: make(map[string]int)}
}

// Observe records one event from an operation's output stream.
//
// Progress is ignored. It is the bulk of the stream and explains nothing that
// the decile logging has not already reported.
// perTitleSkipNotices are makemkvcon's routine "this title was left out"
// messages. A feature disc emits dozens to hundreds of them in one run, and
// none says anything about why a rip failed. Toy Story 4's failure capture was
// ~80 of these and nothing else — the disclosure meant to explain the failure
// was pure noise. They are dropped from the capture; the enumeration and any
// real error are kept, because those are the signal.
var perTitleSkipNotices = map[int]bool{
	3016: true, // Title #X was skipped
	3025: true, // Title #X has length ... less than minimum ... skipped
	3309: true, // Title X is equal to title Y and was skipped
}

func (c *MessageCapture) Observe(ev Event) {
	if ev.Type != "MSG" || ev.Message == nil || ev.Message.Text == "" {
		return
	}
	if perTitleSkipNotices[ev.Message.Code] {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if i, seen := c.index[ev.Message.Text]; seen {
		c.out[i].Count++
		return
	}
	// The cap turns away messages it has not seen before, keeping the earliest.
	// Order matters more than recency here: the enumeration arrives first and is
	// what diagnoses a title that moved, so making room by dropping the oldest
	// would discard the one part worth keeping.
	if len(c.out) >= c.limit {
		c.dropped++
		return
	}
	c.index[ev.Message.Text] = len(c.out)
	c.out = append(c.out, ScanWarning{Code: ev.Message.Code, Text: ev.Message.Text, Count: 1})
}

// Result returns the captured messages in the order they first arrived.
func (c *MessageCapture) Result() []ScanWarning {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ScanWarning(nil), c.out...)
}

// Dropped reports how many distinct messages the cap turned away. Non-zero
// means Result is incomplete, and whatever presents it should say so rather
// than showing a partial list that looks whole.
func (c *MessageCapture) Dropped() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropped
}
