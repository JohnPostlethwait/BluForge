package templates

import (
	"context"
	"strings"
	"testing"
)

// fetchCalls splits a page's inline script at each fetch( so every call's
// option object can be examined on its own.
func fetchCalls(js string) []string {
	parts := strings.Split(js, "fetch(")
	if len(parts) < 2 {
		return nil
	}
	return parts[1:]
}

// Every state-changing request this page makes must carry the CSRF token.
//
// The server used to exempt any non-GET whose Accept header was
// "application/json", on the belief that only this app could send that. Accept
// is CORS-safelisted, so a cross-origin fetch carrying it needs no preflight:
// the browser sends it with the user's cookies and only hides the response.
// With no authentication anywhere in BluForge, that was the whole of the
// defence, and POST /activity/clear-history from any page the user visited
// wiped their rip history.
//
// The exemption is gone and these pages send the token. This is the guard
// against a POST added later that forgets — the failure would be silent in
// development, because a same-origin request works either way until the
// middleware rejects it.
func TestEveryPostFetchCarriesTheCSRFToken(t *testing.T) {
	pages := map[string]string{
		"activity":     inlineScripts(renderActivityWithToken(t)),
		"drive_detail": inlineScripts(renderDriveDetailWithToken(t)),
	}

	for name, js := range pages {
		calls := fetchCalls(js)
		if len(calls) == 0 {
			t.Errorf("%s: no fetch calls found — the extraction is broken, not the page", name)
			continue
		}
		posts := 0
		for i, call := range calls {
			// Only the head of the call holds the options object.
			head := call
			if len(head) > 600 {
				head = head[:600]
			}
			if !strings.Contains(head, "method: 'POST'") {
				continue
			}
			posts++
			if !strings.Contains(head, "X-CSRF-Token") {
				t.Errorf("%s: fetch call %d is a POST with no CSRF token:\n%s", name, i, head)
			}
		}
		if posts == 0 {
			t.Errorf("%s: no POST fetches found; the test is not checking anything", name)
		}
	}
}

// The token must actually reach the page. An empty one would sail past the
// test above and fail every request at runtime.
func TestTheRenderedTokenIsNotEmpty(t *testing.T) {
	for name, html := range map[string]string{
		"activity":     renderActivityWithToken(t),
		"drive_detail": renderDriveDetailWithToken(t),
	} {
		if !strings.Contains(html, "const CSRF_TOKEN = 'test-token-value'") {
			t.Errorf("%s: the rendered page does not carry the token it was given", name)
		}
	}
}

func renderActivityWithToken(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	data := ActivityPageData{StoreJSON: "{}", CSRFToken: "test-token-value"}
	if err := Activity(data).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render activity: %v", err)
	}
	return sb.String()
}

func renderDriveDetailWithToken(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	data := DriveDetailData{DriveIndex: 1, DiscName: "D", CSRFToken: "test-token-value"}
	if err := DriveDetail(data).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render drive detail: %v", err)
	}
	return sb.String()
}
