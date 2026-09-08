package makemkv

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// seqRunner records the order of the makemkvcon calls it is asked to run,
// tagging the drive listing and the scan so ordering can be asserted against
// the pre-scan mount release.
type seqRunner struct {
	output string
	seq    *[]string
}

func (r *seqRunner) Run(_ context.Context, args ...string) (*strings.Reader, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "disc:9999"):
		*r.seq = append(*r.seq, "listdrives")
	case strings.Contains(joined, "info disc:"):
		*r.seq = append(*r.seq, "scan")
	default:
		*r.seq = append(*r.seq, "run:"+joined)
	}
	return strings.NewReader(r.output), nil
}

// A live kernel mount on a disc that then faults is what wedges the drive, so
// BluForge must release any mount it holds on the device before it drives
// makemkvcon against it.
func TestScanReleasesTheDeviceMountBeforeDrivingMakemkvcon(t *testing.T) {
	var seq []string
	runner := &seqRunner{output: twoDriverOutput, seq: &seq}
	ex := NewExecutor(WithRunner(runner), WithUnmountBefore(func(dev string) error {
		seq = append(seq, "unmount:"+dev)
		return nil
	}))

	_, _ = ex.ScanDisc(context.Background(), 1)

	unmountAt, scanAt := -1, -1
	for i, e := range seq {
		if e == "unmount:/dev/sr1" && unmountAt == -1 {
			unmountAt = i
		}
		if e == "scan" && scanAt == -1 {
			scanAt = i
		}
	}
	if unmountAt == -1 {
		t.Fatalf("the device mount was never released before the scan; seq=%v", seq)
	}
	if scanAt == -1 {
		t.Fatalf("the scan never ran; seq=%v", seq)
	}
	if unmountAt > scanAt {
		t.Errorf("the mount was released after the scan started; seq=%v", seq)
	}
}

// A rip drives the disc just as a scan does, so the same invariant holds: if a
// scan's enrichment mount got stuck and stayed tracked, ripping would drive a
// pinned disc and wedge the drive. The rip releases the mount first and refuses
// to run makemkvcon if it cannot be confirmed gone.
func TestRipAbortsWhenTheDeviceMountCannotBeReleased(t *testing.T) {
	runner := &mockCmdRunner{output: twoDriverOutput}
	released := ""
	ex := NewExecutor(WithRunner(runner), WithUnmountBefore(func(dev string) error {
		released = dev
		return errors.New("still mounted at /mnt/sr1")
	}))

	err := ex.StartRip(context.Background(), DiscSource(1), 0, "", t.TempDir(), nil, nil)
	if err == nil {
		t.Fatal("the rip proceeded even though the device mount could not be released")
	}
	if released != "/dev/sr1" {
		t.Fatalf("the rip did not try to release the device mount first (released %q)", released)
	}
}

// If the mount cannot be confirmed gone, driving the disc would wedge the
// drive, so the scan refuses to run makemkvcon at all.
func TestScanAbortsWhenTheDeviceMountCannotBeReleased(t *testing.T) {
	var seq []string
	runner := &seqRunner{output: twoDriverOutput, seq: &seq}
	ex := NewExecutor(WithRunner(runner), WithUnmountBefore(func(dev string) error {
		return errors.New("still mounted at /mnt/sr1")
	}))

	_, err := ex.ScanDisc(context.Background(), 1)
	if err == nil {
		t.Fatal("the scan proceeded even though the device mount could not be released")
	}
	for _, e := range seq {
		if e == "scan" {
			t.Fatalf("makemkvcon scan ran despite an unreleasable mount; seq=%v", seq)
		}
	}
}
