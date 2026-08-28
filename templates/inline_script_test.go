package templates

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)


func renderDashboard(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	if err := Dashboard(DashboardData{StoreJSON: `{"ready":true,"list":[]}`}).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	return sb.String()
}

var scriptRe = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// inlineScripts concatenates every inline <script> body in a rendered page.
func inlineScripts(html string) string {
	var all strings.Builder
	for _, m := range scriptRe.FindAllStringSubmatch(html, -1) {
		all.WriteString(m[1])
		all.WriteString("\n")
	}
	return all.String()
}

// The live pages carry their whole client inside a Go string passed through
// fmt.Sprintf. Nothing in the build looks at it: the Go compiler sees a string,
// templ sees raw HTML, and a stray brace or a dropped parenthesis ships as a
// page that renders and then does nothing at all.
//
// Parsing it with a real JavaScript engine is the cheapest guard available.
// Skipped where node is not installed, so it costs nothing to those without it.
func TestInlinePageScriptsParse(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; skipping the inline-script syntax check")
	}

	pages := map[string]string{
		"drive_detail": inlineScripts(renderDriveDetail(t)),
		"dashboard":    inlineScripts(renderDashboard(t)),
		"activity":     inlineScripts(renderActivity(t)),
	}

	for name, js := range pages {
		if strings.TrimSpace(js) == "" {
			t.Errorf("%s: no inline script found — the extraction is broken, not the page", name)
			continue
		}

		path := filepath.Join(t.TempDir(), name+".js")
		if err := os.WriteFile(path, []byte(js), 0o644); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
			t.Errorf("%s: inline script is not valid JavaScript:\n%s", name, out)
		}
	}
}
