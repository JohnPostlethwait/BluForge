package mpls

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ReadDiscLanguages reads MPLS language metadata from the disc at devicePath.
//
// sourceFiles is an optional list of MPLS filenames (e.g. ["00300.mpls"]) from
// the disc scan.  When provided, only those files are read; when nil or empty,
// all *.mpls files in the playlist directory are read.
//
// Returns a map from MPLS filename (e.g. "00300.mpls") to the PlayItemLanguages
// of its first PlayItem.  Returns a non-nil error if the disc cannot be
// accessed; callers should treat this as a non-fatal condition and skip
// enrichment.
func ReadDiscLanguages(devicePath string, sourceFiles []string) (map[string]PlayItemLanguages, error) {
	mp, err := findMountPoint(devicePath)
	if err != nil {
		return nil, fmt.Errorf("mpls: disc at %s not accessible: %w", devicePath, err)
	}
	slog.Debug("mpls: disc mount point found", "device", devicePath, "mount_point", mp)
	return readFromMountPoint(mp, sourceFiles)
}

// findMountPoint returns the filesystem path where devicePath is mounted.
// On Linux it reads /proc/mounts; on macOS it scans /Volumes for a volume
// containing a BDMV directory.
func findMountPoint(devicePath string) (string, error) {
	switch runtime.GOOS {
	case "linux":
		return findMountLinux(devicePath)
	case "darwin":
		return findMountDarwin()
	default:
		return "", fmt.Errorf("unsupported OS %q", runtime.GOOS)
	}
}

func findMountLinux(devicePath string) (string, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return "", fmt.Errorf("open /proc/mounts: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == devicePath {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("device %s not found in /proc/mounts", devicePath)
}

// findMountDarwin scans /Volumes for a mounted volume that looks like a
// Blu-ray disc (has a BDMV directory).  macOS does not expose device-to-mount
// mappings in a simple text file without calling diskutil, so we use the
// presence of BDMV as a heuristic.
func findMountDarwin() (string, error) {
	entries, err := os.ReadDir("/Volumes")
	if err != nil {
		return "", fmt.Errorf("read /Volumes: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mp := filepath.Join("/Volumes", e.Name())
		if _, err := os.Stat(filepath.Join(mp, "BDMV")); err == nil {
			return mp, nil
		}
	}
	return "", fmt.Errorf("no Blu-ray disc found under /Volumes")
}

// readFromMountPoint reads MPLS files from the mounted disc.  It prefers the
// BDMV/BACKUP/PLAYLIST directory (which holds unencrypted copies) and falls
// back to BDMV/PLAYLIST.
func readFromMountPoint(mountPoint string, sourceFiles []string) (map[string]PlayItemLanguages, error) {
	var playlistDir string
	for _, candidate := range []string{
		filepath.Join(mountPoint, "BDMV", "BACKUP", "PLAYLIST"),
		filepath.Join(mountPoint, "BDMV", "PLAYLIST"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			playlistDir = candidate
			break
		}
	}
	if playlistDir == "" {
		return nil, fmt.Errorf("mpls: no PLAYLIST directory found under %s", mountPoint)
	}

	// Determine which filenames to read.
	filenames := sourceFiles
	if len(filenames) == 0 {
		entries, err := os.ReadDir(playlistDir)
		if err != nil {
			return nil, fmt.Errorf("mpls: read playlist dir: %w", err)
		}
		for _, e := range entries {
			if strings.EqualFold(filepath.Ext(e.Name()), ".mpls") {
				filenames = append(filenames, e.Name())
			}
		}
	}

	result := make(map[string]PlayItemLanguages, len(filenames))
	for _, fn := range filenames {
		path := filepath.Join(playlistDir, fn)
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Debug("mpls: could not read playlist file", "path", path, "error", err)
			continue
		}
		items, err := ParseMPLS(data)
		if err != nil {
			slog.Debug("mpls: parse error", "path", path, "error", err)
			continue
		}
		if len(items) == 0 {
			continue
		}
		// Use the first PlayItem: it corresponds to the primary title content.
		result[fn] = items[0]
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("mpls: no MPLS files could be parsed from %s", playlistDir)
	}
	return result, nil
}
