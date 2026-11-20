package server

import (
	"embed"
	"io/fs"
	"net/http"
)

// The console page is plain HTML, CSS and JavaScript embedded in the binary,
// with no build step (design.md decision 10).
//
//go:embed adminweb
var adminWeb embed.FS

// adminPages serves the console's static files under /admin/.
func (s *Server) adminPages(mux *http.ServeMux) {
	sub, err := fs.Sub(adminWeb, "adminweb")
	if err != nil {
		panic("server: embedded admin page missing: " + err.Error())
	}
	files := http.StripPrefix("/admin/", http.FileServer(http.FS(sub)))
	mux.Handle("GET /admin/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// The page talks only to its own origin and is never framed.
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))
	// /admin without the slash lands on the page.
	mux.Handle("GET /admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
}
