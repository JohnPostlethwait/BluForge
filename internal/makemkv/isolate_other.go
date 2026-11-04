//go:build !linux

package makemkv

import (
	"context"
	"errors"
	"os/exec"
)

var errIsolationUnsupported = errors.New("drive isolation needs Linux mount namespaces")

func configureIsolation(*exec.Cmd) {}

// RunIsolateHelper is unavailable off Linux.
func RunIsolateHelper([]string) error { return errIsolationUnsupported }

// RunIsolateProbe is unavailable off Linux.
func RunIsolateProbe() error { return errIsolationUnsupported }

// ProbeIsolation always fails off Linux, so the executor runs one makemkvcon at
// a time there.
func ProbeIsolation(context.Context) error { return errIsolationUnsupported }
