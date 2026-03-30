package organizer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// MovieMeta holds metadata for a movie title.
type MovieMeta struct {
	Title string
	Year  string
	Part  string
}

// SeriesMeta holds metadata for a TV series episode.
type SeriesMeta struct {
	Show         string
	Season       string
	Episode      string
	EpisodeTitle string
}

// ExtraMeta holds metadata for bonus/extra content.
type ExtraMeta struct {
	Title       string
	Year        string
	Show        string
	Season      string
	ExtraTitle  string
	ContentType string // "Movie" or "Series"
}

// Organizer renders destination paths from Go templates and moves files atomically.
type Organizer struct {
	movieTmpl  *template.Template
	seriesTmpl *template.Template
}

// New parses movieTemplate and seriesTemplate and returns a ready Organizer.
// Templates are text/template strings; they panic on parse failure.
func New(movieTemplate, seriesTemplate string) *Organizer {
	return &Organizer{
		movieTmpl:  template.Must(template.New("movie").Parse(movieTemplate)),
		seriesTmpl: template.Must(template.New("series").Parse(seriesTemplate)),
	}
}

// BuildMoviePath renders the movie template with meta, sanitizes each path component,
// and appends ".mkv". If meta.Part is non-empty, " - Part N" is inserted before the extension.
func (o *Organizer) BuildMoviePath(meta MovieMeta) (string, error) {
	var buf bytes.Buffer
	if err := o.movieTmpl.Execute(&buf, meta); err != nil {
		return "", fmt.Errorf("render movie template: %w", err)
	}

	raw := buf.String()
	sanitized := sanitizePath(raw)

	base := filepath.Base(sanitized)
	dir := filepath.Dir(sanitized)

	if meta.Part != "" {
		base = base + " - Part " + meta.Part
	}
	base = SanitizeFilename(base) + ".mkv"

	return filepath.Join(dir, base), nil
}

// BuildSeriesPath renders the series template with meta, sanitizes each path component,
// and appends ".mkv".
func (o *Organizer) BuildSeriesPath(meta SeriesMeta) (string, error) {
	var buf bytes.Buffer
	if err := o.seriesTmpl.Execute(&buf, meta); err != nil {
		return "", fmt.Errorf("render series template: %w", err)
	}

	raw := buf.String()
	sanitized := sanitizePath(raw)

	return sanitized + ".mkv", nil
}

// BuildUnmatchedPath returns the path for a disc file that could not be matched.
func (o *Organizer) BuildUnmatchedPath(discName, filename string) string {
	return filepath.Join("Unmatched", SanitizeFilename(discName), SanitizeFilename(filename))
}

// BuildExtrasPath returns the path for bonus/extra content.
func (o *Organizer) BuildExtrasPath(meta ExtraMeta) string {
	extraFile := SanitizeFilename(meta.ExtraTitle) + ".mkv"
	if strings.EqualFold(meta.ContentType, "Series") {
		return filepath.Join(
			"TV",
			SanitizeFilename(meta.Show),
			"Season "+SanitizeFilename(meta.Season),
			"Extras",
			extraFile,
		)
	}
	return filepath.Join(
		"Movies",
		SanitizeFilename(meta.Title)+" ("+SanitizeFilename(meta.Year)+")",
		"Extras",
		extraFile,
	)
}

// AtomicMove moves src to dst, creating parent directories as needed.
// It tries os.Rename first (atomic on the same filesystem) and falls back to
// a copy-then-delete for cross-device moves.
func AtomicMove(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Cross-device fallback: copy then delete.
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("cross-device copy: %w", err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove source after copy: %w", err)
	}
	return nil
}

// FileExists reports whether path exists on disk.
func FileExists(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	// Reject symlinks to prevent symlink-based path attacks.
	return info.Mode()&os.ModeSymlink == 0
}

// sanitizePath splits a forward-slash-delimited path, sanitizes each component,
// and re-joins with the OS path separator.
func sanitizePath(p string) string {
	// Normalize to forward slashes so template authors can use "/" regardless of OS.
	parts := strings.Split(filepath.ToSlash(p), "/")
	sanitized := make([]string, 0, len(parts))
	for _, part := range parts {
		s := SanitizeFilename(part)
		if s != "" {
			sanitized = append(sanitized, s)
		}
	}
	return filepath.Join(sanitized...)
}

// copyFile copies the content of src to dst, preserving permissions.
// It refuses to follow symlinks to prevent symlink-based attacks.
func copyFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to copy symlink: %s", src)
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
