package hostedui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesLaunchPage(t *testing.T) {
	handler := Handler()
	cases := []struct {
		path string
		want string
	}{
		{path: "/launch", want: "<!doctype html>"},
		{path: "/launch/", want: "<!doctype html>"},
		{path: "/launch/app.js", want: "predictmarket"},
		{path: "/launch/styles.css", want: ":root"},
		// The page is opened from /launch (no trailing slash), so relative
		// assets resolve to the root path; the API registers these too.
		{path: "/app.js", want: "predictmarket"},
		{path: "/styles.css", want: ":root"},
	}
	for _, tc := range cases {
		request := httptest.NewRequest(http.MethodGet, tc.path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d", tc.path, recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), tc.want) {
			t.Fatalf("%s body does not contain %q", tc.path, tc.want)
		}
		if recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s missing nosniff header", tc.path)
		}
	}
}
