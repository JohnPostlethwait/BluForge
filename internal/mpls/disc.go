package mpls

import (
	"bufio"
	"errors"
	"fmt"
	"io"
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
	root, cleanup, err := OpenDiscRoot(devicePath)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return ReadFrom(root, sourceFiles)
}

// OpenDiscRoot makes the disc at devicePath readable as a directory tree and
// returns its root along with a cleanup function that must always be called.
//
// The mount is owned by the shared MountRegistry, which refcounts concurrent
// readers and — the point of it — takes the mount down when the disc leaves the
// drive, whoever is still holding it. See MountRegistry for what a mount that
// outlives its disc does to a drive.
//
// A device already mounted is adopted rather than reused-and-left: the previous
// behaviour returned a no-op cleanup for it, so a single leaked mount became
// permanent.
func OpenDiscRoot(devicePath string) (string, func(), error) {
	return discMounts.Open(devicePath)
}

// ReadFrom reads MPLS playlists from an accessible disc root — a mount point or
// a plain directory such as a `makemkvcon backup` folder.
//
// sourceFiles behaves as in ReadDiscLanguages: when non-empty only those
// playlist filenames are read, otherwise every *.mpls in the directory is.
func ReadFrom(root string, sourceFiles []string) (map[string]PlayItemLanguages, error) {
	return readFromMountPoint(root, sourceFiles)
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

// readFromMountPoint reads MPLS files from the mounted disc. It tries the
// primary PLAYLIST directory first, then falls back to BACKUP. UHD discs
// sometimes have stub MPLS files in BACKUP with valid PlayItem headers but
// empty STN_Tables (0 audio/subtitle streams), so the primary directory is
// preferred.
func readFromMountPoint(mountPoint string, sourceFiles []string) (map[string]PlayItemLanguages, error) {
	candidates := []string{
		filepath.Join(mountPoint, "BDMV", "PLAYLIST"),
		filepath.Join(mountPoint, "BDMV", "BACKUP", "PLAYLIST"),
	}

	for _, playlistDir := range candidates {
		if _, err := os.Stat(playlistDir); err != nil {
			continue
		}
		slog.Info("mpls: trying playlist directory", "dir", playlistDir)

		result := parseMPLSFromDir(playlistDir, sourceFiles)
		if hasStreams(result) {
			slog.Info("mpls: successfully parsed playlist files",
				"dir", playlistDir, "parsed", len(result))
			return result, nil
		}
		slog.Info("mpls: no usable playlists in directory, trying next",
			"dir", playlistDir, "parsed_but_empty", len(result))
	}

	return nil, fmt.Errorf("mpls: no MPLS files could be parsed from any PLAYLIST directory under %s", mountPoint)
}

// hasStreams reports whether any entry in result contains at least one audio
// or subtitle stream. BACKUP playlist directories on some UHD discs have stub
// MPLS files that parse without error but contain no stream data.
func hasStreams(result map[string]PlayItemLanguages) bool {
	for _, pl := range result {
		if len(pl.Audio) > 0 || len(pl.Subtitle) > 0 {
			return true
		}
	}
	return false
}

// parseMPLSFromDir reads and parses MPLS files from a single directory.
// When sourceFiles is non-empty, only those filenames are read; otherwise
// all .mpls files in the directory are read.
func parseMPLSFromDir(playlistDir string, sourceFiles []string) map[string]PlayItemLanguages {
	filenames := sourceFiles
	if len(filenames) == 0 {
		entries, err := os.ReadDir(playlistDir)
		if err != nil {
			slog.Warn("mpls: could not read playlist directory", "dir", playlistDir, "error", err)
			return nil
		}
		for _, e := range entries {
			if strings.EqualFold(filepath.Ext(e.Name()), ".mpls") {
				filenames = append(filenames, e.Name())
			}
		}
		slog.Info("mpls: discovered playlist files", "dir", playlistDir, "count", len(filenames))
	}

	result := make(map[string]PlayItemLanguages, len(filenames))
	for _, fn := range filenames {
		path := filepath.Join(playlistDir, fn)
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("mpls: could not read playlist file", "path", path, "error", err)
			continue
		}
		items, err := ParseMPLS(data)
		if err != nil {
			slog.Warn("mpls: parse error", "path", path, "error", err)
			continue
		}
		if len(items) == 0 {
			slog.Info("mpls: playlist has 0 PlayItems",
				"path", path, "file_size", len(data))
			continue
		}
		result[fn] = items[0]
	}
	return result
}

// hasContent reports whether a mount point holds anything at all.
//
// An optical disc always has content. An empty directory is a mountpoint with
// no disc behind it: either never mounted, or left over from a device that went
// away. Catching that here means the caller gets "this is not a disc" rather
// than a confusing failure further down about a missing BDMV directory.
func hasContent(root string) error {
	f, err := os.Open(root)
	if err != nil {
		return fmt.Errorf("cannot be read: %w", err)
	}
	defer f.Close()

	names, err := f.Readdirnames(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("cannot be listed: %w", err)
	}
	if len(names) == 0 {
		return errors.New("it is empty (no disc mounted there)")
	}
	return nil
}
