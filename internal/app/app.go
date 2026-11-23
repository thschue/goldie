// Package app is the composition root. It wires modules into roles and is the
// only code that may reach the server from the client side (embedded server).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
	"github.com/thschue/goldie/internal/ui"
)

// route is one public endpoint: a path and the methods it answers. HEAD is
// answered wherever GET is, and is not listed.
type route struct {
	path    string
	methods []string
}

// publicMux is the router of the public listener. api.Server.Register
// registers the spec's operations on it, so the generated code drives routing
// and parameter decoding. It records every route it registers, so the
// route-coverage test reads exactly the table the router is built from; it
// puts every path behind the tokens; and it answers each path's other methods
// with the 405 envelope.
type publicMux struct {
	mux      *http.ServeMux
	tokens   *api.Tokens
	limiter  *api.RateLimiter
	gate     *api.ClientVersionGate
	activity *api.Activity
	routes   []route
	// endStreams ends the open activity streams; Public.Start calls it when
	// its context ends.
	endStreams func()
}

// activityCapacity is how many recent searches and lookups the server keeps
// in its Activity.
const activityCapacity = 1000

// activityKeepalive is how often an open activity stream with nothing to send
// writes a keepalive comment. A variable so tests can shorten it; the router
// reads it when it is built.
var activityKeepalive = api.DefaultActivityKeepalive

// newPublicMuxWith returns the router with the spec's operations over the
// Snapshot provider registered, and nothing else, guarded by a parsed token
// set, a minimum client version (nil for none) and a rate limiter, and
// recording every search and lookup in a bounded Activity. PublicHandler
// builds the public listener with it, and the tests build the bare router
// with it, so the route table they check is the one the server serves.
func newPublicMuxWith(provider func() *engine.Snapshot, tokens *api.Tokens, limiter *api.RateLimiter, gate *api.ClientVersionGate) (*publicMux, error) {
	srv, err := api.NewServer(provider)
	if err != nil {
		return nil, err
	}
	m := &publicMux{mux: http.NewServeMux(), tokens: tokens, limiter: limiter, gate: gate,
		activity: api.NewActivity(activityCapacity, "", nil)}
	srv.RecordInto(m.activity)
	srv.KeepStreamsAlive(activityKeepalive)
	m.endStreams = srv.EndStreams
	srv.Register(m)
	return m, nil
}

// Activity is the Activity the router records every search and lookup into.
func (m *publicMux) Activity() *api.Activity { return m.activity }

// HandleFunc registers one generated operation; its pattern is "METHOD path".
// A pattern without a method is a programming error and panics at startup, as
// net/http does for a malformed pattern.
func (m *publicMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || method == "" || path == "" {
		panic(fmt.Sprintf("app: route pattern %q is not \"METHOD path\"", pattern))
	}
	m.handle(method, path, http.HandlerFunc(h))
}

// handle registers h for method on path and, the first time path is seen, the
// 405 envelope for the path's other methods.
func (m *publicMux) handle(method, path string, h http.Handler) {
	if len(m.methods(path)) == 0 {
		m.mux.Handle(path, m.guard(path, m.methodNotAllowed(path)))
	}
	m.routes = append(m.routes, route{path, []string{method}})
	m.mux.Handle(method+" "+path, m.guard(path, h))
}

// docPaths are the OpenAPI documents. Like the Swagger UI they take the token
// but not the rate limit: they are static, and a token that has used up its
// bucket can still read what the API offers.
var docPaths = map[string]bool{"/openapi.yaml": true, "/openapi.json": true}

// operatorPaths are the operations for operator tokens only. The token check
// comes before the gate and the rate limit, so a reader's 403 costs no token.
var operatorPaths = map[string]bool{"/api/v1/activity": true}

// guard puts every registered path behind the tokens with at least the reader
// Role: the API under /api/, any operation the spec adds later, the OpenAPI
// documents and the Swagger UI (#101). A new route fails closed. API paths are
// then behind the minimum client version and then the per-token rate limit, so
// a request the tokens reject learns nothing of the gate, and neither a 401 nor
// a 426 counts against a bucket. The paths in operatorPaths need
// api.RoleOperator instead; their other methods' 405 does too.
// Under /swagger-ui the Content-Security-Policy wraps the token check, so its
// 401s and 405s carry it too.
func (m *publicMux) guard(path string, h http.Handler) http.Handler {
	switch {
	case path == "/swagger-ui" || strings.HasPrefix(path, "/swagger-ui/"):
		return withCSP(m.tokens.Require(api.RoleReader, h))
	case docPaths[path]:
		return m.tokens.Require(api.RoleReader, h)
	case operatorPaths[path]:
		return m.tokens.Require(api.RoleOperator, m.gate.Require(m.limiter.Limit(h)))
	}
	return m.tokens.Require(api.RoleReader, m.gate.Require(m.limiter.Limit(h)))
}

// RateLimitRejections returns a copy of the requests rejected by the rate
// limit per token name, for goldie.ratelimit.rejections{token} (#13).
func (m *publicMux) RateLimitRejections() map[string]uint64 { return m.limiter.Rejections() }

// methodNotAllowed answers a method that is not registered for path. The Allow
// header lists the registered ones, with HEAD wherever GET is.
func (m *publicMux) methodNotAllowed(path string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var allow []string
		for _, method := range m.methods(path) {
			allow = append(allow, method)
			if method == http.MethodGet {
				allow = append(allow, http.MethodHead)
			}
		}
		w.Header().Set("Allow", strings.Join(allow, ", "))
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed",
			"Use a method listed in the Allow header.")
	})
}

// methods are the methods registered for path, in registration order.
func (m *publicMux) methods(path string) []string {
	var out []string
	for _, r := range m.routes {
		if r.path == path {
			out = append(out, r.methods...)
		}
	}
	return out
}

func (m *publicMux) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.mux.ServeHTTP(w, r) }

// rateLimiter builds the per-token limiter from the RateLimit LoadConfig
// parsed, as is. PublicHandler checks it with checkRateLimit first.
func rateLimiter(l platform.RateLimit) *api.RateLimiter {
	return api.NewRateLimiter(l.Requests, l.Per, l.Burst)
}

// checkRateLimit fails closed on a RateLimit that limits nothing, zero
// requests or no period (the zero value a caller gets by leaving the field
// out): it is an error naming GOLDIE_RATE_LIMIT, not a server without a limit.
func checkRateLimit(l platform.RateLimit) error {
	if l.Requests <= 0 || l.Per <= 0 {
		return fmt.Errorf("%s: no limit was given; the server does not run without one", platform.EnvRateLimit)
	}
	if l.Per/time.Duration(l.Requests) <= 0 {
		return fmt.Errorf("%s: the interval per request rounds to zero; the server does not run without a limit", platform.EnvRateLimit)
	}
	return nil
}

// clientVersionGate builds the minimum client version gate from the Semver
// LoadConfig parsed; the zero Semver is no gate (nil).
func clientVersionGate(minimum platform.Semver) *api.ClientVersionGate {
	if minimum.IsZero() {
		return nil
	}
	return api.NewClientVersionGate(minimum)
}

// apiTokens builds the token set from GOLDIE_API_TOKEN or GOLDIE_API_TOKENS.
// LoadConfig refuses both together; a Config built another way that sets both
// fails the same check here rather than having one ignored. Neither set is
// this role's check: goldie health loads the same Config and needs no token.
// Errors name the variable and never contain a token, a digest or a token's
// length.
func apiTokens(cfg platform.Config) (*api.Tokens, error) {
	if err := platform.CheckAPITokenPairing(cfg.APIToken, cfg.APITokens); err != nil {
		return nil, err
	}
	switch {
	case cfg.APIToken == "" && cfg.APITokens == "":
		return nil, fmt.Errorf("%s or %s is not set: the API needs a token", platform.EnvAPITokens, platform.EnvAPIToken)
	case cfg.APITokens != "":
		tokens, err := api.ParseTokens(cfg.APITokens)
		if err != nil {
			return nil, fmt.Errorf("%s %w", platform.EnvAPITokens, err)
		}
		return tokens, nil
	}
	tokens, err := api.OperatorToken(cfg.APIToken)
	if err != nil {
		return nil, fmt.Errorf("%s %w", platform.EnvAPIToken, err)
	}
	return tokens, nil
}

// content puts the dir Source at GOLDIE_CONTENT_DIR behind a Syncer, like
// every Source, and runs its first poll here, so unloadable content still
// fails at startup. Errors name the variable and never the path.
func content(dir string) (*engine.Syncer, error) {
	if dir == "" {
		return nil, fmt.Errorf("%s is not set: point it at a content directory", platform.EnvContentDir)
	}
	syncer, err := loadDir(context.Background(), dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", platform.EnvContentDir, err)
	}
	return syncer, nil
}

// loadDir puts the dir Source at dir behind a Syncer and runs its first poll.
// The error is the loader's reason, which names a file relative to dir and
// never dir itself; callers say which setting or argument it came from.
func loadDir(ctx context.Context, dir string) (*engine.Syncer, error) {
	syncer := engine.NewSyncer(engine.NewDirSource(dir))
	if err := syncer.Poll(ctx); err != nil {
		return nil, err
	}
	return syncer, nil
}

// contentLoadError is the one error for content that does not load, shared by
// ValidateDir and Local.Start so their messages stay equal: "content does not
// load: " and the loader's reason, never the directory.
func contentLoadError(err error) error {
	return fmt.Errorf("content does not load: %w", err)
}

// ValidateDir runs the content load the server runs at startup over dir, else
// the working directory, and returns nil when it succeeds. A failure reads
// "content does not load: " and the loader's reason, the words server -dev
// ends with; it never names dir.
func ValidateDir(ctx context.Context, dir string) error {
	if dir == "" {
		dir = "."
	}
	if _, err := loadDir(ctx, dir); err != nil {
		return contentLoadError(err)
	}
	return nil
}

// addSwaggerUI registers the Swagger UI paths. They are not API operations, so
// they are not in the spec and not in the route table, but they are registered on
// the same router as the API routes, so the tokens guard them too (#101).
func addSwaggerUI(m *publicMux) error {
	assets, err := contract.SwaggerUIAssets()
	if err != nil {
		return err
	}
	for _, a := range assets {
		h := api.StaticGet(a.ContentType, a.Body)
		if a.Name == "index.html" {
			m.handle(http.MethodGet, "/swagger-ui", h)
			m.handle(http.MethodGet, "/swagger-ui/{$}", h)
			continue
		}
		m.handle(http.MethodGet, "/swagger-ui/"+a.Name, h)
	}
	return nil
}

// addUI mounts the embedded UI as the catch-all at "/". It is not registered
// through m.handle: it is no API operation, so it stays out of the route table
// and the spec. Paths that must never fall back to the UI are claimed first:
// the Swagger UI prefix (unknown paths, or every path when it is switched off,
// keep the token and the 404 envelope) and the probe paths, which only the
// admin listener serves (PC-01), so a monitor pointed at this port never reads
// a UI page as "ready". Matching is exact and case-sensitive.
func addUI(m *publicMux, notFound http.Handler, logger *slog.Logger) {
	if !ui.Built() {
		logger.Warn("ui not built: serving the placeholder page; run ./run ui")
	}
	if len(m.methods("/swagger-ui")) == 0 {
		m.mux.Handle("/swagger-ui", m.guard("/swagger-ui", notFound))
	}
	m.mux.Handle("/swagger-ui/{rest...}", m.guard("/swagger-ui/", notFound))
	for _, p := range []string{"/livez", "/readyz", "/version"} {
		m.mux.Handle(p, notFound)
	}
	m.mux.Handle("/", ui.Handler(ui.Assets()))
}

// swaggerUICSP is the Content-Security-Policy of the Swagger UI paths. The
// page takes bearer tokens, so scripts and requests stay on this origin, eval
// and framing are off, and <base> cannot move relative URLs. Styles allow
// 'unsafe-inline' because Swagger UI sets inline styles; the stylesheet's
// icons are data: images.
const swaggerUICSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// withCSP adds the Swagger UI Content-Security-Policy to every response of h.
func withCSP(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", swaggerUICSP)
		h.ServeHTTP(w, r)
	})
}

// ConfigError is a PublicHandler failure the operator fixes through a setting:
// a missing or malformed token, a rate limit that limits nothing, or content
// that is missing or does not load. It holds every such problem PublicHandler
// found, joined with errors.Join, one per line. Any other PublicHandler error
// is internal.
type ConfigError struct{ err error }

func (e *ConfigError) Error() string { return e.err.Error() }

func (e *ConfigError) Unwrap() error { return e.err }

// sourceDirCheck is the name of the dir Source's readiness check (PC-02).
const sourceDirCheck = "source:dir"

// dirPollInterval is how often the dir Source is polled for changed content.
const dirPollInterval = 30 * time.Second

// Public is the handler of the public listener and the owner of its Source's
// Syncer.
// It embeds the router, so it is an http.Handler and counts rate limit rejections.
// Until Start runs, it serves the content PublicHandler loaded.
type Public struct {
	*publicMux
	syncer  *engine.Syncer
	mu      sync.Mutex
	started bool
	loops   sync.WaitGroup // the poll loop Start runs
}

// Start runs the dir Source's poll loop until ctx ends, ends the open activity
// streams when it does, and registers its
// readiness check, "source:dir", with reg. It does not block. The check is
// ready while the Syncer holds a Snapshot, so a stale Source stays ready and
// no error reaches /readyz (PC-02). Only the first call with a live context
// starts a loop; a call with a cancelled context does not use up the start.
// Every call registers the check, which replaces itself.
func (p *Public) Start(ctx context.Context, reg *platform.Registry) {
	reg.Add(sourceDirCheck, func(context.Context) error {
		if p.syncer.Current() == nil {
			return errors.New("no snapshot")
		}
		return nil
	})
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started || ctx.Err() != nil {
		return
	}
	p.started = true
	p.loops.Go(func() { p.syncer.Run(ctx, dirPollInterval, nil) })
	// The first stop signal ends the activity streams: Shutdown waits for
	// open requests and never cancels them, so a stream would hold it for
	// the whole grace period (PC-08).
	context.AfterFunc(ctx, p.endStreams)
}

// PublicHandler builds the handler for the public listener (GOLDIE_ADDR) from
// the Config LoadConfig returned, used as is, and the server's component
// logger (nil discards). It loads the dir Source and serves the API under
// /api/, every path of which requires a configured token; the OpenAPI document
// at /openapi.yaml and /openapi.json and, unless switched off, Swagger UI at
// /swagger-ui, which take the same token; and MCP over streamable HTTP at
// /mcp, which needs a token too. Anything else is the embedded UI (a placeholder
// page until ./run ui has built it), except /livez, /readyz and /version, which
// are the not-found envelope here (PC-01). A
// missing or bad token, a rate limit that limits nothing and unloadable
// content are reported together in one *ConfigError, each naming its variable
// and none its value, so the server fails at startup (PC-04) and the operator
// fixes them all in one restart.
// Platform stays unaware of the contract: this is where the two meet.
func PublicHandler(cfg platform.Config, logger *slog.Logger) (*Public, error) {
	var errs []error
	tokens, err := apiTokens(cfg)
	if err != nil {
		errs = append(errs, err)
	}
	if err := checkRateLimit(cfg.RateLimit); err != nil {
		errs = append(errs, err)
	}
	syncer, err := content(cfg.ContentDir)
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, &ConfigError{errors.Join(errs...)}
	}
	return newPublic(cfg, tokens, syncer, logger)
}

// newPublic builds the public handler over a loaded Syncer and a parsed token
// set; cfg supplies the rate limit, the minimum client version and the
// Swagger UI switch. PublicHandler and Local share it, so a local server
// serves exactly what goldie server serves.
func newPublic(cfg platform.Config, tokens *api.Tokens, syncer *engine.Syncer, logger *slog.Logger) (*Public, error) {
	m, err := newPublicMuxWith(syncer.Current, tokens, rateLimiter(cfg.RateLimit), clientVersionGate(cfg.MinClientVersion))
	if err != nil {
		return nil, err
	}
	if cfg.SwaggerUIEnabled {
		if err := addSwaggerUI(m); err != nil {
			return nil, err
		}
	}
	if logger == nil {
		logger = discardLogger
	}
	addMCP(m, m, logger)
	notFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, http.StatusNotFound, "not_found", "no such endpoint",
			"Check the request path against the OpenAPI document.")
	})
	// Unknown /api/ paths need the token too, so they reveal nothing.
	m.mux.Handle("/api/", m.guard("/api/", notFound))
	m.mux.Handle("/api", m.guard("/api", notFound))
	addUI(m, notFound, logger)
	return &Public{publicMux: m, syncer: syncer}, nil
}
