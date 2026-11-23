package ui

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// Acceptance tests for #164 (ADR 0019): the static handler of the embedded UI.
//
// Contract fixed here:
//
//	func Handler(fsys fs.FS) http.Handler  // panics at construction on an unknown extension
//	func Assets() fs.FS                    // the root that is served: has index.html
//	func Built() bool                      // true when dist/app/index.html is embedded
//
// An unknown file extension in the FS is a programming or build error, not a
// request error, and the issue fixes Handler's signature without an error
// result, so Handler panics at construction (the precedent is
// publicMux.HandleFunc). The panic is what makes startup fail.

const (
	indexHTML = "<!doctype html><title>goldie</title><script src=\"/assets/app.js\"></script>"
	appJS     = "console.log('app');"
	appCSS    = "body{margin:0}"

	// uiCSP is the Content-Security-Policy of every UI path, character for character.
	uiCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
		"connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'"

	immutable = "public, max-age=31536000, immutable"
	secret    = "TOPSECRET-OUTSIDE-THE-FS"
)

func spaFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":         {Data: []byte(indexHTML)},
		"assets/app.js":      {Data: []byte(appJS)},
		"assets/app.css":     {Data: []byte(appCSS)},
		"assets/logo.svg":    {Data: []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"/>")},
		"assets/logo.png":    {Data: []byte("\x89PNG\r\n\x1a\n")},
		"assets/font.woff2":  {Data: []byte("wOF2")},
		"assets/data.json":   {Data: []byte("{}")},
		"favicon.ico":        {Data: []byte("\x00\x00\x01\x00")},
		"assets/deep/x.js":   {Data: []byte("1")},
		"assets/deep/y/z.js": {Data: []byte("2")},
	}
}

// serve runs one request against h with the URL path set as given (raw is the
// escaped form), without going through net/http's own path cleaning, so the
// handler has to defend itself.
func serve(t *testing.T, h http.Handler, method, raw string) *httptest.ResponseRecorder {
	t.Helper()
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		t.Fatalf("bad test path %q: %v", raw, err)
	}
	r := httptest.NewRequestWithContext(t.Context(), method, "/", nil)
	r.URL = &url.URL{Path: decoded, RawPath: raw}
	r.RequestURI = raw
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func get(t *testing.T, h http.Handler, raw string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, h, http.MethodGet, raw)
}

func mustHandler(t *testing.T, fsys fs.FS) http.Handler {
	t.Helper()
	h := Handler(fsys)
	if h == nil {
		t.Fatal("Handler returned nil")
	}
	return h
}

func wantHeader(t *testing.T, what string, rec *httptest.ResponseRecorder, name, want string) {
	t.Helper()
	if got := rec.Header().Get(name); got != want {
		t.Errorf("%s: %s = %q, want %q", what, name, got, want)
	}
}

func isHTML(rec *httptest.ResponseRecorder) bool {
	return strings.Contains(rec.Header().Get("Content-Type"), "text/html") ||
		strings.Contains(strings.ToLower(rec.Body.String()), "<html") ||
		strings.Contains(strings.ToLower(rec.Body.String()), "<!doctype")
}

// --- what is served, and which is built -------------------------------------

func TestAssetsHasAnIndexAndBuiltSaysWhichOne(t *testing.T) {
	assets := Assets()
	if assets == nil {
		t.Fatal("Assets() is nil")
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("Assets() has no index.html: %v", err)
	}
	placeholder, err := os.ReadFile("dist/placeholder/index.html")
	if err != nil {
		t.Fatalf("the placeholder is committed at internal/ui/dist/placeholder/index.html: %v", err)
	}
	for _, want := range []string{
		"<title>goldie (UI not built)</title>",
		"<code>./run ui</code>",
		"Node",
		`<meta name="viewport" content="width=device-width, initial-scale=1">`,
		`<meta name="color-scheme" content="light dark">`,
	} {
		if !strings.Contains(string(placeholder), want) {
			t.Errorf("placeholder/index.html does not contain %q: %s", want, placeholder)
		}
	}
	if _, err := os.Stat("dist/placeholder.html"); err == nil {
		t.Error("dist/placeholder.html still exists: the placeholder moved to dist/placeholder/index.html")
	}
	if Built() {
		if string(index) == string(placeholder) {
			t.Errorf("Built() is true but Assets() serves the placeholder")
		}
		return
	}
	if string(index) != string(placeholder) {
		t.Errorf("Built() is false: Assets() index.html is not the placeholder.\n got: %s\nwant: %s", index, placeholder)
	}
}

func TestBuiltIsTrueExactlyWhenTheBuildIsEmbedded(t *testing.T) {
	_, err := os.Stat("dist/app/index.html")
	if built := err == nil; built != Built() {
		t.Errorf("Built() = %v, but dist/app/index.html exists = %v", Built(), built)
	}
}

func TestTheBuildOutputIsGitignored(t *testing.T) {
	b, err := os.ReadFile("../../.gitignore")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		switch strings.TrimSpace(line) {
		case "internal/ui/dist/app/", "/internal/ui/dist/app/", "internal/ui/dist/app", "/internal/ui/dist/app":
			return
		}
	}
	t.Error(".gitignore does not ignore internal/ui/dist/app/ (ADR 0019: the build output is not committed)")
}

func TestHandlerOverAssetsServesTheRoot(t *testing.T) {
	h := mustHandler(t, Assets())
	for _, p := range []string{"/", "/index.html", "/projects/anything"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK || !isHTML(rec) {
			t.Errorf("GET %s over Assets(): status %d, content type %q, want 200 HTML", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

// --- the happy path and the fallback ----------------------------------------

func TestServesIndexAtTheRootAndAtIndexHTML(t *testing.T) {
	h := mustHandler(t, spaFS())
	for _, p := range []string{"/", "/index.html"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200 (no redirect)", p, rec.Code)
		}
		if rec.Body.String() != indexHTML {
			t.Errorf("GET %s: body %q, want index.html", p, rec.Body)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("GET %s: Location %q, want none", p, loc)
		}
		wantHeader(t, p, rec, "Content-Type", "text/html; charset=utf-8")
		wantHeader(t, p, rec, "Cache-Control", "no-cache")
	}
}

func TestExtensionlessPathsFallBackToIndex(t *testing.T) {
	h := mustHandler(t, spaFS())
	for _, p := range []string{
		"/projects", "/projects/github.com/acme/shop", "/articles/some-id", "/v1.2/projects",
		"/projects/my%20proj", "/a/b/c/d/e/f", "/.well-known/thing", "/unknown",
	} {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK || rec.Body.String() != indexHTML {
			t.Errorf("GET %s: status %d body %q, want 200 and index.html", p, rec.Code, rec.Body)
		}
		wantHeader(t, p, rec, "Content-Type", "text/html; charset=utf-8")
		wantHeader(t, p, rec, "Cache-Control", "no-cache")
		wantHeader(t, p, rec, "Content-Security-Policy", uiCSP)
		wantHeader(t, p, rec, "X-Content-Type-Options", "nosniff")
	}
}

func TestQueryStringsDoNotChangeWhatIsServed(t *testing.T) {
	h := mustHandler(t, spaFS())
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/app.js?v=1&x=%2e%2e%2f", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || rec.Body.String() != appJS {
		t.Errorf("status %d body %q, want 200 and app.js", rec.Code, rec.Body)
	}
}

func TestHeadIsServedWithTheHeadersAndNoBody(t *testing.T) {
	h := mustHandler(t, spaFS())
	for _, p := range []string{"/", "/projects", "/assets/app.js"} {
		rec := serve(t, h, http.MethodHead, p)
		if rec.Code != http.StatusOK {
			t.Errorf("HEAD %s: status %d, want 200", p, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("HEAD %s: body of %d bytes, want none", p, rec.Body.Len())
		}
		full := get(t, h, p)
		for _, name := range []string{"Content-Type", "Cache-Control", "Content-Security-Policy", "X-Content-Type-Options"} {
			if rec.Header().Get(name) != full.Header().Get(name) {
				t.Errorf("HEAD %s: %s = %q, GET has %q", p, name, rec.Header().Get(name), full.Header().Get(name))
			}
		}
	}
}

func TestMissingFilesWithAnExtensionAreNotFoundNeverHTML(t *testing.T) {
	h := mustHandler(t, spaFS())
	for _, p := range []string{
		"/assets/x.js", "/assets/missing.css", "/favicon.png", "/robots.txt", "/x.php", "/.env",
		"/assets/deep/nope.js", "/projects/v1.2", "/index.htm", "/assets/app.js.map",
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := serve(t, h, method, p)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: status %d, want 404", method, p, rec.Code)
			}
			if isHTML(rec) {
				t.Errorf("%s %s: a missing file got HTML: %q", method, p, rec.Body)
			}
			if strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
				t.Errorf("%s %s: a 404 is marked immutable: %q", method, p, rec.Header().Get("Cache-Control"))
			}
			wantHeader(t, method+" "+p, rec, "X-Content-Type-Options", "nosniff")
		}
	}
}

func TestFaviconIsServedWhenItExistsAnd404WhenItDoesNot(t *testing.T) {
	rec := get(t, mustHandler(t, spaFS()), "/favicon.ico")
	if rec.Code != http.StatusOK {
		t.Errorf("present /favicon.ico: status %d, want 200", rec.Code)
	}
	without := spaFS()
	delete(without, "favicon.ico")
	rec = get(t, mustHandler(t, without), "/favicon.ico")
	if rec.Code != http.StatusNotFound || isHTML(rec) {
		t.Errorf("absent /favicon.ico: status %d body %q, want 404 and no HTML", rec.Code, rec.Body)
	}
}

// --- methods ----------------------------------------------------------------

type envelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Hint string `json:"hint"`
		} `json:"details"`
	} `json:"error"`
}

func TestOtherMethodsGetTheMethodNotAllowedEnvelope(t *testing.T) {
	h := mustHandler(t, spaFS())
	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodTrace,
		http.MethodConnect, "PROPFIND", "get",
	} {
		for _, p := range []string{"/", "/projects", "/assets/app.js", "/assets/missing.js", "/index.html"} {
			rec := serve(t, h, method, p)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status %d, want 405", method, p, rec.Code)
				continue
			}
			wantHeader(t, method+" "+p, rec, "Allow", "GET, HEAD")
			wantHeader(t, method+" "+p, rec, "X-Content-Type-Options", "nosniff")
			if mt := rec.Header().Get("Content-Type"); !strings.HasPrefix(mt, "application/json") {
				t.Errorf("%s %s: Content-Type %q, want application/json", method, p, mt)
			}
			var env envelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Errorf("%s %s: body is not the error envelope: %v: %s", method, p, err, rec.Body)
				continue
			}
			if env.Error.Code != "method_not_allowed" || env.Error.Message == "" || env.Error.Details.Hint == "" {
				t.Errorf("%s %s: envelope %+v, want code method_not_allowed with a message and details.hint", method, p, env.Error)
			}
			if strings.Contains(rec.Body.String(), p) && p != "/" {
				t.Errorf("%s %s: the envelope echoes the path: %s", method, p, rec.Body)
			}
		}
	}
}

// --- headers ----------------------------------------------------------------

func TestAssetsAreImmutableAndTypedExplicitly(t *testing.T) {
	h := mustHandler(t, spaFS())
	for p, wantType := range map[string]string{
		"/assets/app.js":      "text/javascript; charset=utf-8",
		"/assets/app.css":     "text/css; charset=utf-8",
		"/assets/logo.svg":    "image/svg+xml",
		"/assets/logo.png":    "image/png",
		"/assets/font.woff2":  "font/woff2",
		"/assets/data.json":   "application/json",
		"/assets/deep/x.js":   "text/javascript; charset=utf-8",
		"/assets/deep/y/z.js": "text/javascript; charset=utf-8",
	} {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", p, rec.Code)
			continue
		}
		wantHeader(t, p, rec, "Content-Type", wantType)
		wantHeader(t, p, rec, "Cache-Control", immutable)
		wantHeader(t, p, rec, "X-Content-Type-Options", "nosniff")
		wantHeader(t, p, rec, "Content-Security-Policy", uiCSP)
	}
}

func TestEveryServedFileHasAnExplicitTypeAndNosniff(t *testing.T) {
	h := mustHandler(t, spaFS())
	for name := range spaFS() {
		rec := get(t, h, "/"+name)
		if rec.Code != http.StatusOK {
			t.Errorf("GET /%s: status %d, want 200", name, rec.Code)
			continue
		}
		if rec.Header().Get("Content-Type") == "" {
			t.Errorf("GET /%s: no Content-Type; it must never be sniffed", name)
		}
		wantHeader(t, "/"+name, rec, "X-Content-Type-Options", "nosniff")
	}
	if ct := get(t, h, "/favicon.ico").Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Errorf("/favicon.ico Content-Type %q, want an image type", ct)
	}
}

func TestOnlyTheAssetsDirectoryIsImmutable(t *testing.T) {
	h := mustHandler(t, spaFS())
	for _, p := range []string{"/", "/index.html", "/projects", "/favicon.ico"} {
		if cc := get(t, h, p).Header().Get("Cache-Control"); strings.Contains(cc, "immutable") || strings.Contains(cc, "max-age=31536000") {
			t.Errorf("GET %s: Cache-Control %q, want no long-lived caching outside /assets/", p, cc)
		}
	}
}

func TestAnUnknownExtensionInTheFSFailsAtConstruction(t *testing.T) {
	for _, bad := range []string{"assets/payload.exe", "assets/x.php", "script.xyz", "assets/deep/er/evil.html5x", "a.b.unknownext"} {
		fsys := spaFS()
		fsys[bad] = &fstest.MapFile{Data: []byte("x")}
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Handler accepted a FS holding %q: an unknown extension must fail startup (panic at construction)", bad)
				}
			}()
			_ = Handler(fsys)
		}()
	}
}

func TestEveryKnownExtensionIsAcceptedAtConstruction(t *testing.T) {
	// Handler must not panic over the files a Vite build produces.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Handler panicked on a FS of known extensions: %v", r)
		}
	}()
	_ = Handler(spaFS())
}

// --- no listings, no traversal ----------------------------------------------

func TestDirectoriesAreNeverListed(t *testing.T) {
	h := mustHandler(t, spaFS())
	for _, p := range []string{"/assets", "/assets/", "/assets/deep", "/assets/deep/", "/assets/deep/y", "/assets/deep/y/"} {
		rec := get(t, h, p)
		body := rec.Body.String()
		for _, name := range []string{"app.js", "app.css", "logo.svg", "x.js", "z.js"} {
			if strings.Contains(body, name) && body != indexHTML {
				t.Errorf("GET %s lists a directory entry %q: %s", p, name, body)
			}
		}
		switch rec.Code {
		case http.StatusNotFound, http.StatusBadRequest:
		case http.StatusOK:
			if body != indexHTML {
				t.Errorf("GET %s: 200 with %q, want the index page or an error", p, body)
			}
		default:
			t.Errorf("GET %s: status %d, want 200 (index), 400 or 404", p, rec.Code)
		}
		if rec.Header().Get("Location") != "" {
			t.Errorf("GET %s redirects to %q", p, rec.Header().Get("Location"))
		}
	}
}

// hostilePaths are escaped request paths. None may read a file, fall back to
// the index page or reach the file outside the FS.
var hostilePaths = []string{
	"/../secret.txt", "/..", "/../", "/assets/../../secret.txt", "/assets/../index.html", "/./index.html", "/assets/./app.js",
	"/%2e%2e/secret.txt", "/%2E%2E/secret.txt", "/%2e%2e%2fsecret.txt", "/..%2fsecret.txt", "/..%2Fsecret.txt",
	"/assets/..%2f..%2fsecret.txt", "/assets/%2e%2e/%2e%2e/secret.txt", "/%252e%252e/secret.txt", "/.%2e/secret.txt",
	"/assets%2fapp.js", "/assets%2Fapp.js", "/%2fsecret.txt", "/projects%2fx", "/a%2f..%2f..%2fsecret.txt",
	"//secret.txt", "//", "///", "/assets//app.js", "/projects//x",
	"/\\..\\secret.txt", "/..\\secret.txt", "/assets\\app.js", "/assets%5capp.js", "/%5c..%5csecret.txt", "/projects\\x",
	"/a%00b", "/%00", "/assets/app.js%00.png", "/index.html%00", "/secret.txt%00.js",
	"/%c0%ae%c0%ae/secret.txt", "/..%c0%afsecret.txt", "/%ef%bc%8e%ef%bc%8e/secret.txt",
	"/" + strings.Repeat("a", 8192), "/" + strings.Repeat("a/", 4096), "/assets/" + strings.Repeat("../", 4096) + "secret.txt",
	"/" + strings.Repeat("%2e%2e/", 2048) + "secret.txt",
}

func outsideFS(t *testing.T) (fsys fs.FS, root string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(root+"/secret.txt", []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root+"/app/assets", 0o750); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"index.html": indexHTML, "assets/app.js": appJS} {
		if err := os.WriteFile(root+"/app/"+name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return os.DirFS(root + "/app"), root
}

func TestHostilePathsAreRejectedAndReadNothingOutsideTheFS(t *testing.T) {
	fsys, root := outsideFS(t)
	h := mustHandler(t, fsys)
	for _, p := range hostilePaths {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := serve(t, h, method, p)
			show := p
			if len(show) > 60 {
				show = show[:60] + "..."
			}
			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound && rec.Code != http.StatusRequestURITooLong {
				t.Errorf("%s %s: status %d, want 400, 404 or 414 (rejected)", method, show, rec.Code)
			}
			body := rec.Body.String()
			if body == indexHTML || body == appJS {
				t.Errorf("%s %s: a hostile path was served a page: %q", method, show, body)
			}
			if strings.Contains(body, secret) {
				t.Errorf("%s %s: read a file outside the FS", method, show)
			}
			for _, leak := range []string{root, os.TempDir(), "secret.txt", "/app/", ".go:", "goroutine"} {
				if strings.Contains(body, leak) || strings.Contains(rec.Header().Get("Location"), leak) {
					t.Errorf("%s %s: the response leaks %q: %s", method, show, leak, body)
				}
			}
			if rec.Header().Get("Location") != "" {
				t.Errorf("%s %s: redirects to %q; a hostile path is rejected, not cleaned", method, show, rec.Header().Get("Location"))
			}
			if method == http.MethodGet && len(body) > 4096 {
				t.Errorf("%s %s: a %d-byte error body", method, show, len(body))
			}
		}
	}
}

func TestHostilePathsAreRejectedWithTheNonGetMethodsToo(t *testing.T) {
	// A hostile path with a method the UI refuses is still never read: 405 or
	// a rejection, nothing from outside the FS.
	fsys, _ := outsideFS(t)
	h := mustHandler(t, fsys)
	for _, p := range hostilePaths {
		rec := serve(t, h, http.MethodPost, p)
		if rec.Code >= 500 || rec.Code < 400 || strings.Contains(rec.Body.String(), secret) {
			t.Errorf("POST %.40s: status %d, want a 4xx without file content", p, rec.Code)
		}
	}
}

func TestAnErrorNeverNamesTheFSOrTheRequest(t *testing.T) {
	fsys, root := outsideFS(t)
	h := mustHandler(t, fsys)
	for _, p := range []string{"/assets/missing-file.js", "/missing-file.css"} {
		rec := get(t, h, p)
		body := rec.Body.String()
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", p, rec.Code)
		}
		for _, leak := range []string{root, "missing-file", "no such file", "open "} {
			if strings.Contains(body, leak) {
				t.Errorf("GET %s: the 404 leaks %q: %s", p, leak, body)
			}
		}
	}
}

func TestTheHandlerKeepsNoStateBetweenRequests(t *testing.T) {
	h := mustHandler(t, spaFS())
	first := get(t, h, "/assets/app.js").Body.String()
	_ = get(t, h, "/../x")
	_ = serve(t, h, http.MethodPost, "/")
	if again := get(t, h, "/assets/app.js").Body.String(); again != first || again != appJS {
		t.Errorf("a later request got %q, want %q", again, appJS)
	}
}

// --- hardening headers on every UI response ---------------------------------

// wantHardening checks the headers every UI response carries, whatever its status.
func wantHardening(t *testing.T, what string, h http.Header) {
	t.Helper()
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'none'") {
		t.Errorf("%s: CSP %q lacks form-action 'none'", what, csp)
	}
	for name, want := range map[string]string{
		"Referrer-Policy":            "no-referrer",
		"Cross-Origin-Opener-Policy": "same-origin",
		"X-Content-Type-Options":     "nosniff",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s: %s = %q, want %q", what, name, got, want)
		}
	}
	wantDenyAllPermissions(t, what, h.Get("Permissions-Policy"))
}

// wantDenyAllPermissions requires every directive to have an empty allowlist
// and the powerful features a browser would otherwise grant to be named.
func wantDenyAllPermissions(t *testing.T, what, pp string) {
	t.Helper()
	if strings.TrimSpace(pp) == "" {
		t.Errorf("%s: no Permissions-Policy", what)
		return
	}
	seen := map[string]bool{}
	for _, d := range strings.Split(pp, ",") {
		feature, list, ok := strings.Cut(strings.TrimSpace(d), "=")
		if !ok || strings.TrimSpace(list) != "()" {
			t.Errorf("%s: Permissions-Policy directive %q does not deny the feature with ()", what, d)
		}
		seen[strings.TrimSpace(feature)] = true
	}
	for _, f := range []string{"camera", "microphone", "geolocation", "payment", "usb"} {
		if !seen[f] {
			t.Errorf("%s: Permissions-Policy %q does not deny %s", what, pp, f)
		}
	}
}

func TestEveryUIResponseCarriesTheHardeningHeaders(t *testing.T) {
	h := mustHandler(t, spaFS())
	paths := []string{
		"/", "/index.html", "/projects", "/projects/github.com/acme/shop", // index and SPA fallback
		"/assets/app.js", "/assets/app.css", "/assets/logo.svg", "/favicon.ico", // files
		"/assets/missing.js", "/robots.txt", "/.env", // 404
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, p := range paths {
			rec := serve(t, h, method, p)
			wantHardening(t, method+" "+p, rec.Header())
		}
	}
}

func TestRefusedUIRequestsCarryTheHardeningHeadersToo(t *testing.T) {
	h := mustHandler(t, spaFS())
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions} {
		for _, p := range []string{"/", "/assets/app.js", "/assets/missing.js"} {
			rec := serve(t, h, method, p)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status %d, want 405", method, p, rec.Code)
			}
			wantHardening(t, method+" "+p, rec.Header())
		}
	}
	for _, p := range []string{"/../secret.txt", "/%2e%2e/secret.txt", "/assets%2fapp.js", "/a%00b"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := serve(t, h, method, p)
			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: status %d, want a rejection", method, p, rec.Code)
			}
			wantHardening(t, method+" "+p, rec.Header())
		}
	}
}

// A nested id travels as one %2F segment (ADR 0016): the article route must
// reach the SPA, while an escaped slash or dot segment elsewhere stays rejected.
func TestItemRoutesWithAnEscapedSlashFallBackToIndex(t *testing.T) {
	h := mustHandler(t, spaFS())
	for _, p := range []string{"/items/article/platform%2Fdeploy", "/items/article/x%2F%3Cimg%20src%3Dx%20onerror%3Dalert(1)%3E"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK || !isHTML(rec) {
			t.Errorf("GET %s: status %d, want the index page", p, rec.Code)
		}
	}
	for _, p := range []string{"/items/%2e%2e/secret.txt", "/items/article/%2e%2e%2f%2e%2e%2fsecret.txt", "/items/a%00b", "/items/article/a%2Fb.js"} {
		if rec := get(t, h, p); rec.Code == http.StatusOK {
			t.Errorf("GET %s: status 200, want a rejection", p)
		}
	}
}
