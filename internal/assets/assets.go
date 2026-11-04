// Package assets builds URLs for BluForge's static files that change whenever
// the file does.
//
// The server sends no cache headers for /static, so a browser may keep an old
// style.css after a deploy and serve it across an ordinary refresh. That hid a
// whole layout change on Jotunheim until a hard refresh. A URL carrying a hash
// of the file's contents is a new URL whenever the file changes, so the browser
// fetches it fresh.
package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// dir is where the server serves /static from (see web.Server).
var dir = "static"

var (
	mu       sync.Mutex
	versions = map[string]string{}
)

// URL returns the /static URL for name, with a version taken from the file's
// contents: "/static/style.css?v=3f2a9c1e0b7d". The hash is read once per
// process; the files ship in the image and do not change while it runs. A file
// that cannot be read gets the plain URL, so a missing asset fails the way it
// always did rather than breaking the page.
func URL(name string) string {
	mu.Lock()
	v, ok := versions[name]
	if !ok {
		v = hashFile(filepath.Join(dir, name))
		versions[name] = v
	}
	mu.Unlock()

	if v == "" {
		return "/static/" + name
	}
	return "/static/" + name + "?v=" + v
}

func hashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}
