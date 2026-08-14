package templates

import (
	"strings"
	"testing"
)

// The scan output is available whether or not anything went wrong. Nesting it
// inside the warning card meant it could only be read when BluForge had already
// decided something was wrong — which is the case where it is least needed.
func TestTheScanOutputIsAvailableWithoutAWarning(t *testing.T) {
	html := renderDriveDetail(t)

	block := strings.Index(html, "scanOutput")
	if block < 0 {
		t.Fatal("the scan output is not on the page")
	}

	// It must not sit inside the block gated on there being findings.
	warning := strings.Index(html, "scanDiagnosis || {}).findings) || []).length > 0")
	if warning >= 0 && block > warning {
		if end := strings.Index(html[warning:], "scanOutput"); end >= 0 {
			// Both present: confirm the output has its own visibility gate
			// rather than inheriting the warning card's.
			if !strings.Contains(html, `x-show="($store.drive.scanOutput || []).length > 0"`) {
				t.Error("the scan output has no gate of its own, so it shows only when a warning does")
			}
		}
	}
}

// It is a disclosure, not a wall of text: a scan says a great many ordinary
// things and none of them should be the first thing on the page.
func TestTheScanOutputIsCollapsed(t *testing.T) {
	html := renderDriveDetail(t)

	i := strings.Index(html, "What MakeMKV reported during the scan")
	if i < 0 {
		t.Fatal("the scan output has no summary line to open")
	}
	// The <details> wrapper must come before its summary.
	before := html[:i]
	if !strings.Contains(before[max(0, len(before)-400):], "<details") {
		t.Error("the scan output is not inside a details disclosure")
	}
}
