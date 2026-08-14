package templates

import (
	"strings"
	"testing"
)

// An estimate arrives once enough has been copied to average out, measured in
// bytes rather than percent.
//
// Five percent of an 86 GB disc is 4.3 GB — about five minutes showing nothing
// but "estimating…", which is indistinguishable from a stall and cost a rip
// that was cancelled because it looked stuck. Five percent of a DVD is over in
// seconds. A fixed amount read gives every disc the same short warm-up.
func TestTheEtaAppearsAfterAFixedAmountIsCopied(t *testing.T) {
	html := renderActivity(t)

	if !strings.Contains(html, "500 * 1024 * 1024") {
		t.Error("the estimate is not gated on an amount copied")
	}
	if strings.Contains(html, "if (j.progress >= 5) {") {
		t.Error("the estimate is still gated on a percentage of the disc")
	}
}

// A disc whose size is unknown has no byte count to wait for, and must still
// get an estimate rather than none at all.
func TestADiscOfUnknownSizeStillGetsAnEstimate(t *testing.T) {
	html := renderActivity(t)

	if !strings.Contains(html, "(!j.sizeBytes && j.progress >= 5)") {
		t.Error("a disc of unknown size has no path to an estimate")
	}
}

// Dividing by a progress of zero yields Infinity, which renders as a duration.
func TestTheEstimateIsNotComputedFromZeroProgress(t *testing.T) {
	html := renderActivity(t)

	if !strings.Contains(html, "enough && j.progress > 0") {
		t.Error("the estimate can be computed before any progress is reported")
	}
}
