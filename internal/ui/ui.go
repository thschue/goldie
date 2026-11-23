package ui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// dist holds the committed placeholder page (dist/placeholder/index.html) and, when ./run ui has run, the
// SPA build in dist/app (gitignored, ADR 0019).
//
//go:embed all:dist
var dist embed.FS

const (
	builtIndex = "dist/app/index.html"
	maxPathLen = 2048

	// csp is the Content-Security-Policy of every UI response. The SPA loads
	// only its own files and talks only to this origin's API.
	csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
		"connect-src 'self'; base-uri 'none'; frame-ancestors 'none'"

	assetCache = "public, max-age=31536000, immutable"
)

// contentTypes is the closed set of files the SPA may contain. Handler panics
// on any other extension, and every response names its type, so nothing is
// sniffed.
var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
}

// Built reports whether the SPA build is embedded, as opposed to the placeholder.
func Built() bool {
	_, err := fs.Stat(dist, builtIndex)
	return err == nil
}

// Assets is the file tree to serve: the SPA build when embedded, otherwise a
// tree holding only the placeholder as index.html.
func Assets() fs.FS {
	if Built() {
		sub, err := fs.Sub(dist, "dist/app")
		if err != nil {
			panic("ui: " + err.Error()) // the path is a constant
		}
		return sub
	}
	sub, err := fs.Sub(dist, "dist/placeholder")
	if err != nil {
		panic("ui: " + err.Error()) // the path is a constant
	}
	return sub
}

type file struct {
	body         []byte
	contentType  string
	cacheControl string
}

type handler struct{ files map[string]file }

// Handler serves fsys as a single-page app: a file by its exact path, and
// index.html for any other extensionless GET or HEAD path (client-side routes).
// It reads fsys once, here, and panics when fsys has no index.html or holds a
// file with an extension outside the content-type table: both are build errors,
// and the panic fails startup.
func Handler(fsys fs.FS) http.Handler {
	files := map[string]file{}
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ct, ok := contentTypes[strings.ToLower(path.Ext(name))]
		if !ok {
			return fmt.Errorf("no content type for file %q", name)
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		cc := "no-cache"
		if strings.HasPrefix(name, "assets/") {
			cc = assetCache
		}
		files[name] = file{body, ct, cc}
		return nil
	})
	if _, ok := files["index.html"]; err == nil && !ok {
		err = fmt.Errorf("no index.html")
	}
	if err != nil {
		panic("ui: " + err.Error())
	}
	return handler{files}
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", "Use GET or HEAD.")
		return
	}
	name, ok := cleanName(r.URL)
	if !ok {
		writeError(w, r, http.StatusBadRequest, "bad_request", "invalid path",
			"Request a path without dot segments or special characters.")
		return
	}
	f, found := h.files[name]
	if !found {
		if path.Ext(path.Base(name)) != "" {
			writeError(w, r, http.StatusNotFound, "not_found", "not found", "Check the file name.")
			return
		}
		f = h.files["index.html"] // a client-side route
	}
	hd := w.Header()
	hd.Set("Content-Type", f.contentType)
	hd.Set("Cache-Control", f.cacheControl)
	hd.Set("Content-Security-Policy", csp)
	hd.Set("Content-Length", strconv.Itoa(len(f.body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(f.body)
	}
}

// cleanName turns the request path into a name in the file table. It rejects,
// instead of cleaning, every path that is not already in canonical form: an
// escaped slash or other escape the router would decode, dot segments, empty
// segments, backslashes and control characters. One trailing slash is allowed.
func cleanName(u *url.URL) (string, bool) {
	p := u.Path
	if len(p) > maxPathLen || p == "" || p[0] != '/' {
		return "", false
	}
	if u.RawPath != "" && u.RawPath != (&url.URL{Path: p}).EscapedPath() {
		return "", false
	}
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < 0x20 || c == 0x7f || c == '\\' {
			return "", false
		}
	}
	name := p[1:]
	if t := strings.TrimSuffix(name, "/"); t != "" {
		name = t
	}
	if name == "" {
		return "index.html", true
	}
	if !fs.ValidPath(name) {
		return "", false
	}
	return name, true
}

// writeError writes the error envelope (ADR 0014). ui cannot import api, so the
// shape is repeated here; message and hint never echo the request.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message, hint string) {
	body, err := json.Marshal(map[string]any{"error": map[string]any{
		"code": code, "message": message, "details": map[string]string{"hint": hint},
	}})
	if err != nil {
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
