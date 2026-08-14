package makemkv

// ScanWarning is a message from a scan that the user ought to see.
type ScanWarning struct {
	Code int    `json:"code"`
	Text string `json:"text"`
	// Count is how many times the message repeated. A disc with unreadable
	// sectors emits the same read error dozens of times as the drive retries.
	Count int `json:"count"`
}

// routineScanMessages are the codes a healthy scan emits as a matter of course.
//
// Classification is deliberately by exclusion rather than by a list of known
// problems: a message nobody has catalogued is exactly the case where staying
// silent costs the user content without telling them. The cost of the choice is
// occasional noise from a benign code not yet listed here, which is the
// cheaper mistake.
var routineScanMessages = map[int]bool{
	1005: true, // MakeMKV started
	1011: true, // Using LibreDrive mode
	3006: true, // Opening files on harddrive
	3007: true, // Using direct disc access mode
	3025: true, // Title shorter than the configured minimum — a setting, not a fault
	3305: true, // AACS directory not present, assuming unencrypted disc
	3307: true, // File X was added as title #N
	5005: true, // N titles saved
	5011: true, // Operation successfully completed
	5014: true, // Saving N titles into directory
	5036: true, // Copy complete
	5042: true, // No usable optical drives — noise on every invocation
	5085: true, // Loaded content hash table
}

// ScanWarnings extracts the messages from a scan that indicate something went
// wrong, collapsing repeats.
//
// A disc with unreadable sectors reports every retry, and titles it could not
// read are simply absent from the results — so without this the user sees a
// tidy list of titles and no indication that content was dropped.
func ScanWarnings(messages []Message) []ScanWarning {
	var out []ScanWarning
	index := make(map[string]int, len(messages))

	for _, m := range messages {
		// Suppressed messages are logged and never shown: they describe this
		// installation rather than the disc in the drive.
		if routineScanMessages[m.Code] || suppressedMessage(m) {
			continue
		}
		key := m.Text
		if i, seen := index[key]; seen {
			out[i].Count++
			continue
		}
		index[key] = len(out)
		out = append(out, ScanWarning{Code: m.Code, Text: m.Text, Count: 1})
	}

	return out
}

// ScanOutput is everything MakeMKV said during a scan, in order, with repeats
// collapsed.
//
// Unlike ScanWarnings this filters nothing. It is shown behind a disclosure the
// user opens deliberately, so there is no cost to including the ordinary lines
// — and every time a scan has been questioned, the answer was in a message
// somebody had decided was not worth showing.
func ScanOutput(messages []Message) []ScanWarning {
	var out []ScanWarning
	index := make(map[string]int, len(messages))

	for _, m := range messages {
		if m.Text == "" {
			continue
		}
		if i, seen := index[m.Text]; seen {
			out[i].Count++
			continue
		}
		index[m.Text] = len(out)
		out = append(out, ScanWarning{Code: m.Code, Text: m.Text, Count: 1})
	}

	return out
}
