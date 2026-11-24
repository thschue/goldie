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
	gmcp "github.com/thschue/goldie/internal/mcp"
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
	// operatorPaths are the paths the spec reserves for operator tokens.
	operatorPaths map[string]bool
	// anonymous are the operations the spec marks `security: []`, as "METHOD /path".
	anonymous map[string]bool
	// signIn caps failed sign-ins per remote address (#489).
	signIn *api.SignInLimiter
	// sync serves the sync operations; newPublic binds it to the Source's
	// Syncer.
	sync   *syncControl
	routes []route
	// endStreams ends the open activity streams; Public.Start calls it when
	// its context ends.
	endStreams func()
}

// activityCapacity is how many recent searches and lookups the server keeps
// in its Activity.
const activityCapacity = 1000

// newPublicMuxKeepalive returns the router with the spec's operations over the
// Snapshot provider registered, and nothing else, guarded by a parsed token
// set, a minimum client version (nil for none) and a rate limiter, and
// recording every search and lookup in a bounded Activity named after this
// replica (activityReplicaName). An open activity stream with nothing to send
// writes a keepalive comment every keepalive (zero or less keeps
// api.DefaultActivityKeepalive); opts go last. PublicHandler builds the public listener
// with it, and the tests build the bare router with it, so the route table
// they check is the one the server serves.
func newPublicMuxKeepalive(provider func() *engine.Snapshot, tokens *api.Tokens, limiter *api.RateLimiter, gate *api.ClientVersionGate, keepalive time.Duration, opts ...api.ServerOption) (*publicMux, error) {
	activity := api.NewActivity(activityCapacity, activityReplicaName(), nil)
	ctl := &syncControl{}
	srv, err := api.NewServer(provider, append([]api.ServerOption{api.RecordInto(activity), api.WithSync(ctl), api.WithSessions(tokens)}, opts...)...)
	if err != nil {
		return nil, err
	}
	operatorPaths, err := contract.OperatorPaths()
	if err != nil {
		return nil, err
	}
	anonymous, err := contract.AnonymousOperations()
	if err != nil {
		return nil, err
	}
	m := &publicMux{mux: http.NewServeMux(), tokens: tokens, limiter: limiter, gate: gate, activity: activity, operatorPaths: operatorPaths, anonymous: anonymous, signIn: api.NewSignInLimiter(), sync: ctl}
	srv.KeepStreamsAlive(keepalive)
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
	m.mux.Handle(method+" "+path, m.guardOperation(method, path, h))
}

// guardOperation is guard for one operation. The session resource's
// operations the spec marks `security: []` (sign-in and sign-out) take no
// bearer token and go through api.SessionEntry; its others are guard's, with
// no caching.
func (m *publicMux) guardOperation(method, path string, h http.Handler) http.Handler {
	switch {
	case m.anonymous[method+" "+path]:
		if method == http.MethodPost && path == api.SessionPath {
			h = m.signIn.Limit(h)
		}
		return api.SessionEntry(h)
	case path == api.SessionPath:
		return api.NoStore(m.guard(path, h))
	}
	return m.guard(path, h)
}

// docPaths are the OpenAPI documents. Like the Swagger UI they take the token
// but not the rate limit: they are static, and a token that has used up its
// bucket can still read what the API offers.
var docPaths = map[string]bool{"/openapi.yaml": true, "/openapi.json": true}

// guard puts every registered path behind the tokens with at least the reader
// Role: the API under /api/, any operation the spec adds later, the OpenAPI
// documents and the Swagger UI (#101). A new route fails closed. API paths are
// then behind the minimum client version and then the per-token rate limit, so
// a request the tokens reject learns nothing of the gate, and neither a 401 nor
// a 426 counts against a bucket. The paths the spec marks `x-goldie-role:
// operator` (contract.OperatorPaths) need api.RoleOperator instead, checked
// before the gate and the rate limit so a reader's 403 costs no token; their
// other methods' 405 does too.
// The "/" catch-all (the UI) is public by design and does not go through guard.
// Under /swagger-ui the Content-Security-Policy wraps the token check, so its
// 401s and 405s carry it too.
func (m *publicMux) guard(path string, h http.Handler) http.Handler {
	switch {
	case path == "/swagger-ui" || strings.HasPrefix(path, "/swagger-ui/"):
		return withCSP(m.tokens.Require(api.RoleReader, h))
	case docPaths[path]:
		return m.tokens.Require(api.RoleReader, h)
	case m.operatorPaths[path]:
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

// checkSessionKey fails a GOLDIE_SESSION_KEY the session layer would refuse,
// so it is reported with the other configuration errors. Unset is fine: the
// server then makes a random key. The error never contains the key.
func checkSessionKey(cfg platform.Config) error {
	if _, err := (*api.Tokens)(nil).WithSessions(cfg.SessionKey, false); err != nil {
		return fmt.Errorf("%s %w", platform.EnvSessionKey, err)
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

// content puts the dir Source at GOLDIE_CONTENT_DIR (set) behind a Syncer,
// like every Source, and runs its first poll here, so unloadable content
// still fails at startup. Errors name the variable and never the path.
func content(dir string) (*engine.Syncer, error) {
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
	return loadSource(ctx, engine.NewDirSource(dir))
}

// loadSource puts src behind a Syncer and runs its first poll.
func loadSource(ctx context.Context, src engine.Source) (*engine.Syncer, error) {
	syncer := engine.NewSyncer(src)
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
// "content does not load: " and the loader's reason, the words server --dev
// ends with; it never names dir.
func ValidateDir(ctx context.Context, dir string) error {
	_, err := ValidateDirWarnings(ctx, dir)
	return err
}

// ValidateDirWarnings is ValidateDir that also returns the load's warnings,
// the findings that do not fail it, such as a suppress reference that names
// nothing. Each names a file relative to dir, is printable ASCII and at most
// 1 KiB, so a caller may print it as it is.
func ValidateDirWarnings(ctx context.Context, dir string) ([]string, error) {
	if dir == "" {
		dir = "."
	}
	syncer, err := loadDir(ctx, dir)
	if err != nil {
		return nil, contentLoadError(err)
	}
	return syncer.Status().Warnings, nil
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
// and the spec. newPublic claims /api and /swagger-ui first, so they never fall
// back to the UI; addUI claims the probe paths, which only the admin listener
// serves (PC-01), so a monitor pointed at this port never reads a UI page as
// "ready". Matching is exact and case-sensitive.
func addUI(m *publicMux, notFound http.Handler, logger *slog.Logger) {
	if !ui.Built() {
		logger.Warn("ui not built: serving the placeholder page; run ./run ui")
	}
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
// a missing or malformed token, a rate limit that limits nothing, no Source,
// content that does not load, or a git Source setting the engine refuses. It
// holds every such problem PublicHandler found, joined with errors.Join, one
// per line. Any other PublicHandler error is internal.
type ConfigError struct{ err error }

func (e *ConfigError) Error() string { return e.err.Error() }

func (e *ConfigError) Unwrap() error { return e.err }

// Public is the handler of the public listener and the owner of its Source's
// Syncer.
// It embeds the router, so it is an http.Handler and counts rate limit rejections.
// Until Start runs, it serves the content PublicHandler loaded: the dir
// Source's, or none for the git Source, which answers 503 not_ready.
type Public struct {
	*publicMux
	source
	pollLog *slog.Logger                // logs every poll, when the Source asks for it; nil logs none
	polls   *platform.PollCounter       // counts and times every poll (goldie.syncs, goldie.sync.duration); nil counts none
	poll    time.Duration               // GOLDIE_POLL_INTERVAL
	probe   func(context.Context) error // the upstream check of GOLDIE_MODE=ui and mcp, which have no Source; nil otherwise
	mu      sync.Mutex
	started bool
	loops   sync.WaitGroup // the poll loop Start runs
}

// Start runs the Source's poll loop, at GOLDIE_POLL_INTERVAL, until ctx ends,
// ends the open activity streams when it does, and registers the Source's
// readiness check, "source:dir" or "source:git", with reg. It does not block:
// the git Source's first load is the loop's first poll. The check fails until
// the Syncer holds a Snapshot and then stays ready, so a stale Source stays
// ready and no error reaches /readyz (PC-02). The git Source's polls are
// logged (logPolls). Only the first call with a live context
// starts a loop; a call with a cancelled context does not use up the start.
// Every call registers the check, which replaces itself.
func (p *Public) Start(ctx context.Context, reg *platform.Registry) {
	if p.probe != nil {
		reg.Add(p.check, p.probe)
	} else {
		reg.Add(p.check, func(context.Context) error {
			if p.syncer.Current() == nil {
				return errors.New("no snapshot")
			}
			return nil
		})
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started || ctx.Err() != nil {
		return
	}
	p.started = true
	if p.probe != nil {
		// Nothing to poll: only the proxied streams end with the server.
		context.AfterFunc(ctx, p.endStreams)
		return
	}
	onPoll := p.pollHook(ctx)
	p.sync.run(ctx, onPoll)
	p.loops.Go(func() { p.syncer.Run(ctx, p.poll, onPoll) })
	// The first stop signal ends the activity streams: Shutdown waits for
	// open requests and never cancels them, so a stream would hold it for
	// the whole grace period (PC-08).
	context.AfterFunc(ctx, p.endStreams)
}

// pollHook is the poll loop's onPoll: it logs polls when the Source asks for
// it (logPolls) and counts each finished poll in goldie.syncs and times it in goldie.sync.duration. A poll that
// ends with ctx, the server stopping, is no result and is not counted. It is
// nil when there is nothing to do.
func (p *Public) pollHook(ctx context.Context) func(engine.SyncStatus) {
	var logged func(engine.SyncStatus)
	if p.pollLog != nil {
		logged = logPolls(ctx, p.pollLog)
	}
	if logged == nil && p.polls == nil {
		return nil
	}
	return func(st engine.SyncStatus) {
		if logged != nil {
			logged(st)
		}
		if ctx.Err() == nil {
			p.polls.Add(ctx, st.LastError != "", st.Duration)
		}
	}
}

// PublicHandler builds the handler for the public listener (GOLDIE_ADDR) from
// the Config LoadConfig returned, used as is, and the server's component
// logger (nil discards). It loads the dir Source, or builds the git Source
// without loading it (Start does), and serves the API under
// /api/, every path of which requires a configured token; the OpenAPI document
// at /openapi.yaml and /openapi.json and, unless switched off, Swagger UI at
// /swagger-ui, which take the same token; and MCP over streamable HTTP at
// /mcp, which needs a token too. Anything else is the embedded UI (a placeholder
// page until ./run ui has built it), except /livez, /readyz and /version, which
// are the not-found envelope here (PC-01). A
// missing or bad token, a rate limit that limits nothing, no Source or two,
// unloadable dir content and every git Source setting the engine refuses are
// reported together in one *ConfigError, each naming its variable
// and none its value, so the server fails at startup (PC-04) and the operator
// fixes them all in one restart.
// Platform stays unaware of the contract: this is where the two meet.
func PublicHandler(cfg platform.Config, logger *slog.Logger) (*Public, error) {
	return PublicHandlerWith(cfg, logger, nil)
}

// PublicHandlerWith is PublicHandler for a server that exports metrics through
// tel (nil for none): every search and lookup it answers is counted by hit or
// miss (goldie.searches, goldie.lookups) and every finished poll of its Source
// by result (goldie.syncs).
func PublicHandlerWith(cfg platform.Config, logger *slog.Logger, tel *platform.Telemetry) (*Public, error) {
	if cfg.Mode == platform.ModeUI {
		return newPublicUI(cfg, logger)
	}
	if cfg.Mode == platform.ModeMCP {
		return newPublicMCP(cfg)
	}
	var errs []error
	tokens, err := apiTokens(cfg)
	if err != nil {
		errs = append(errs, err)
	}
	if err := checkRateLimit(cfg.RateLimit); err != nil {
		errs = append(errs, err)
	}
	if err := checkSessionKey(cfg); err != nil {
		errs = append(errs, err)
	}
	src, err := serveSource(cfg)
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, &ConfigError{errors.Join(errs...)}
	}
	opt, calls := resultCounters(tel)
	p, err := newPublicCounted(false, cfg, tokens, src, logger, api.DefaultActivityKeepalive, calls, opt)
	if p != nil {
		p.polls = tel.PollCounter("goldie.syncs", "Source polls finished, by source and result.", src.name)
		tel.ItemGauge(itemCounts(src.syncer.Current))
	}
	return p, err
}

// itemCounts reads the counts of the current Snapshot for goldie.items. Before
// the first Snapshot it returns nil, so nothing is observed.
func itemCounts(current func() *engine.Snapshot) func() []platform.ItemCount {
	return func() []platform.ItemCount {
		snap := current()
		if snap == nil {
			return nil
		}
		counts := snap.ItemCounts()
		out := make([]platform.ItemCount, len(counts))
		for i, c := range counts {
			out[i] = platform.ItemCount{Scope: c.Scope, Kind: c.Kind, Count: c.Count}
		}
		return out
	}
}

// resultCounters is the option that counts searches and lookups by result
// (goldie.searches, goldie.lookups) and the skill gets that hit
// (goldie.skill.gets) through tel, and the counter of MCP tool calls
// (goldie.mcp.tool.calls) that addMCP hands to each MCP server.
func resultCounters(tel *platform.Telemetry) (api.ServerOption, *platform.ToolCallCounter) {
	results := api.CountResults(
		tel.ResultCounter("goldie.searches", "Searches answered, by result."),
		tel.ResultCounter("goldie.lookups", "Article lookups answered, by result."))
	gets := api.CountSkillGets(tel.SkillGetCounter("goldie.skill.gets", "Skill gets that hit, by skill name, scope and client."))
	calls := tel.ToolCallCounter("goldie.mcp.tool.calls", "MCP tool calls, by tool and result.")
	return func(s *api.Server) { results(s); gets(s) }, calls
}

// newPublic builds the public handler over a Source and a parsed token set;
// keepalive is the activity stream keepalive; cfg supplies the rate limit, the
// minimum client version, the Swagger UI switch and the poll interval. PublicHandler and Local share it, so a local
// server serves exactly what goldie server serves.
func newPublic(cfg platform.Config, tokens *api.Tokens, src source, logger *slog.Logger, keepalive time.Duration, opts ...api.ServerOption) (*Public, error) {
	return newPublicSessions(false, cfg, tokens, src, logger, keepalive, opts...)
}

// newPublicSessions is newPublic; dev is true only for the loopback dev
// server Local starts, whose plain-http session cookies drop Secure and
// __Host-. It is never read from a request.
func newPublicSessions(dev bool, cfg platform.Config, tokens *api.Tokens, src source, logger *slog.Logger, keepalive time.Duration, opts ...api.ServerOption) (*Public, error) {
	return newPublicCounted(dev, cfg, tokens, src, logger, keepalive, nil, opts...)
}

// newPublicCounted is newPublicSessions with calls, the counter of MCP tool
// calls (nil counts nothing).
func newPublicCounted(dev bool, cfg platform.Config, tokens *api.Tokens, src source, logger *slog.Logger, keepalive time.Duration, calls *platform.ToolCallCounter, opts ...api.ServerOption) (*Public, error) {
	if logger == nil {
		logger = discardLogger
	}
	tokens, err := tokens.WithSessions(cfg.SessionKey, dev)
	if err != nil {
		return nil, &ConfigError{fmt.Errorf("%s %w", platform.EnvSessionKey, err)}
	}
	m, err := newPublicMuxKeepalive(src.syncer.Current, tokens, rateLimiter(cfg.RateLimit), clientVersionGate(cfg.MinClientVersion), keepalive, serverOptions(cfg, logger, opts)...)
	if err != nil {
		return nil, err
	}
	if cfg.SwaggerUIEnabled {
		if err := addSwaggerUI(m); err != nil {
			return nil, err
		}
	}
	full := cfg.Mode != platform.ModeAPI
	if full {
		addMCP(m, m, logger, gmcp.CountToolCalls(calls))
	}
	notFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, http.StatusNotFound, "not_found", "no such endpoint",
			"Check the request path against the OpenAPI document.")
	})
	// Unknown /api/ paths need the token too, so they reveal nothing.
	m.mux.Handle("/api/", m.guard("/api/", notFound))
	m.mux.Handle("/api", m.guard("/api", notFound))
	// The Swagger UI prefix: unknown paths, or every path when it is switched
	// off, keep the token and the 404 envelope instead of falling back to the UI.
	if len(m.methods("/swagger-ui")) == 0 {
		m.mux.Handle("/swagger-ui", m.guard("/swagger-ui", notFound))
	}
	m.mux.Handle("/swagger-ui/{rest...}", m.guard("/swagger-ui/", notFound))
	if full {
		addUI(m, notFound, logger)
	} else {
		// api mode: /mcp and / answer the 404 envelope; nothing falls back to the UI.
		m.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			api.WriteError(w, http.StatusNotFound, "not_found", "no such endpoint",
				"This server runs with GOLDIE_MODE=api and does not serve /mcp or the UI.")
		}))
	}
	m.sync.bind(src.syncer)
	p := &Public{publicMux: m, source: src}
	if src.logAttrs != nil {
		p.pollLog = logger.With(src.logAttrs...)
	}
	// LoadConfig never yields less than the floor; a Config built by hand
	// (zero) gets it too, never a busy loop.
	p.poll = max(cfg.PollInterval, minPollInterval)
	return p, nil
}

// serverOptions are the options newPublic gives the API server: the
// instruction variables from the configuration, the activity federation, then
// the caller's.
func serverOptions(cfg platform.Config, logger *slog.Logger, opts []api.ServerOption) []api.ServerOption {
	out := []api.ServerOption{api.WithInstructionVars(engine.InstructionVars{MCPName: cfg.MCPName, CLI: "goldie"}), api.WithSwaggerUI(cfg.SwaggerUIEnabled), api.WithObservabilityLinks(cfg.GrafanaURL, cfg.TraceURLTemplate)}
	out = append(out, activityFederation(cfg, logger)...)
	return append(out, opts...)
}
