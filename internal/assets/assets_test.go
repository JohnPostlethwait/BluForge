package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func useDir(t *testing.T, d string) {
	t.Helper()
	prevDir, prevVersions := dir, versions
	dir, versions = d, map[string]string{}
	t.Cleanup(func() { dir, versions = prevDir, prevVersions })
}

// A changed file gets a different URL, so a browser holding the old one
// fetches the new one.
func TestURLChangesWhenTheFileChanges(t *testing.T) {
	d := t.TempDir()
	useDir(t, d)
	if err := os.WriteFile(filepath.Join(d, "style.css"), []byte("a{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := URL("style.css")

	useDir(t, d) // a new process, after a deploy
	if err := os.WriteFile(filepath.Join(d, "style.css"), []byte("a{display:grid}"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := URL("style.css")

	if !strings.HasPrefix(first, "/static/style.css?v=") {
		t.Errorf("URL = %q, want /static/style.css?v=<hash>", first)
	}
	if first == second {
		t.Errorf("the URL did not change with the file: %q", first)
	}
}

// The same contents give the same URL, so an unchanged file stays cached.
func TestURLIsStableForTheSameFile(t *testing.T) {
	d := t.TempDir()
	useDir(t, d)
	if err := os.WriteFile(filepath.Join(d, "logo.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := URL("logo.svg")
	useDir(t, d)
	if b := URL("logo.svg"); a != b {
		t.Errorf("URL changed for an unchanged file: %q then %q", a, b)
	}
}

func TestURLOfAMissingFileIsThePlainPath(t *testing.T) {
	useDir(t, t.TempDir())
	if got := URL("nope.css"); got != "/static/nope.css" {
		t.Errorf("URL = %q, want /static/nope.css", got)
	}
}
