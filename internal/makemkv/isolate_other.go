//go:build !linux

package makemkv

import (
	"context"
	"errors"
)

var errIsolationUnsupported = errors.New("drive isolation needs Linux mount namespaces")

// RunIsolateHelper is unavailable off Linux.
func RunIsolateHelper([]string) error { return errIsolationUnsupported }

// RunIsolateProbe is unavailable off Linux.
func RunIsolateProbe() error { return errIsolationUnsupported }

// ProbeIsolation always fails off Linux, so the executor runs one makemkvcon at
// a time there.
func ProbeIsolation(context.Context) error { return errIsolationUnsupported }
