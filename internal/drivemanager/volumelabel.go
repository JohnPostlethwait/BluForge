package drivemanager

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// volumeLabeler reads the volume label of the disc in one device.
//
// makemkvcon names a disc only through a listing that probes every drive, so
// it has to wait for all of them. The label is the same name — makemkvcon's
// disc name is the UDF volume label — and reading it touches only the one
// device, so a disc can be named while other drives are busy.
type volumeLabeler interface {
	Label(ctx context.Context, devicePath string) (string, error)
}

// labelTimeout bounds a label read. blkid reads a handful of sectors; a drive
// still spinning up can take a few seconds to answer them.
const labelTimeout = 15 * time.Second

// blkidLabeler reads labels with blkid. -p probes the device directly rather
// than trusting blkid's cache, which knows nothing of a disc just inserted.
type blkidLabeler struct{}

func (blkidLabeler) Label(ctx context.Context, devicePath string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, labelTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "blkid", "-p", "-s", "LABEL", "-o", "value", devicePath).Output()
	if err != nil {
		return "", fmt.Errorf("blkid %s: %w", devicePath, err)
	}
	label := strings.TrimSpace(string(out))
	if label == "" {
		return "", errors.New("the disc has no volume label")
	}
	return label, nil
}
