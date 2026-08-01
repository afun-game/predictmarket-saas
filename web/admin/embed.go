// Package adminui serves the no-build admin console for the PredictMarket
// SaaS platform. The single-page app uses hash routing, so every non-asset
// request maps to index.html.
package adminui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed index.html app.js styles.css
var files embed.FS

// Handler serves the admin console assets. The console root and any hash
// route map to index.html; app.js and styles.css are served verbatim.
func Handler() http.Handler {
	sub, err := fs.Sub(files, ".")
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		name := strings.TrimPrefix(r.URL.Path, "/admin")
		name = strings.Trim(name, "/")
		if name == "" || name == "index.html" {
			name = "index.html"
		}
		data, err := fs.ReadFile(sub, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType(name))
		_, _ = w.Write(data)
	})
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	default:
		return "text/html; charset=utf-8"
	}
}
