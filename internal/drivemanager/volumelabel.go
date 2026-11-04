package drivemanager

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
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

// blkidLabeler reads labels with blkid.
//
// -p probes the device directly rather than trusting blkid's cache, which knows
// nothing of a disc just inserted. -n udf,iso9660 limits the probe to the two
// filesystems a disc's label comes from. Without it blkid tries every
// superblock and RAID signature it knows, some of which sit at the end of the
// device, and seeking a Blu-ray to its outer edge is slow. On Jotunheim's BU40N
// the full probe took 7.5s on a spun-up disc and the limited one 0.7s.
//
// There is no timeout of our own. A 15s limit used to kill the read, and it
// fired on a healthy drive still spinning a just-inserted disc up. The disc was
// then left unnamed until another drive's rip finished, most of two hours.
// Killing blkid does not make a slow read any faster, and a read stuck in the
// kernel does not die of it, so the limit only ever cost the answer. ctx still
// stops it at shutdown.
type blkidLabeler struct{}

// blkidArgs is the probe, kept apart so a test can check it.
func blkidArgs(devicePath string) []string {
	return []string{"-p", "-n", "udf,iso9660", "-s", "LABEL", "-o", "value", devicePath}
}

func (blkidLabeler) Label(ctx context.Context, devicePath string) (string, error) {
	out, err := exec.CommandContext(ctx, "blkid", blkidArgs(devicePath)...).Output()
	if err != nil {
		return "", fmt.Errorf("blkid %s: %w", devicePath, err)
	}
	label := strings.TrimSpace(string(out))
	if label == "" {
		return "", errors.New("the disc has no volume label")
	}
	return label, nil
}
