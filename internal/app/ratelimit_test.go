package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thschue/goldie/internal/contract/contracttest"
	"github.com/thschue/goldie/internal/platform"
)

// Per-token rate limiting (#100). Config.RateLimit carries GOLDIE_RATE_LIMIT
// as LoadConfig parsed it, the default (20/s,burst=40) already applied (#178):
// PublicHandler uses the typed value as is and parses nothing, except that a
// RateLimit of zero requests fails closed (#186,
// TestZeroValueRateLimitFailsClosed). The helpers below fill an unset
// RateLimit with the default, as LoadConfig does. The tests
// write a limit as the operator does, "<n>/s" or "<n>/m", optionally
// ",burst=<b>", and get the value through LoadConfig. Every token name has its
// own bucket, held in memory by the handler PublicHandler returns, so a new
// handler (a restarted process) starts with full buckets. Only requests the
// token guard lets through count; a rejection is a 429 in the error envelope
// with code rate_limited and a Retry-After header in whole seconds, rounded
// up and never 0.
//
// Until #13's metrics exist, the handler counts rejections per token name for
// goldie.ratelimit.rejections{token}: it implements rejectionCounter, and
// RateLimitRejections returns a copy of the counts since it was built, keyed
// by token name and never by a secret.
const envRateLimit = "GOLDIE_RATE_LIMIT"

// loadedRateLimit is the RateLimit LoadConfig reads from GOLDIE_RATE_LIMIT=spec,
// "" for unset, the way the server hands it to PublicHandler.
func loadedRateLimit(t *testing.T, spec string) platform.RateLimit {
	t.Helper()
	env := map[string]string{}
	if spec != "" {
		env[envRateLimit] = spec
	}
	cfg, err := platform.LoadConfig(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatalf("LoadConfig with %s=%q: %v", envRateLimit, spec, err)
	}
	return cfg.RateLimit
}

type rejectionCounter interface {
	RateLimitRejections() map[string]uint64
}

// rateHandler builds the public handler with only opts' tokens configured.
func rateHandler(t *testing.T, opts platform.Config) http.Handler {
	t.Helper()
	opts.ContentDir = testContent
	opts.RateLimit = withDefaultRateLimit(opts.RateLimit)
	h, err := PublicHandler(opts, nil)
	if err != nil {
		t.Fatalf("PublicHandler: %v", err)
	}
	return h
}

// hit serves one GET in process; token "" sends no Authorization header.
func hit(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	if token != "" {
		bearer(token)(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// rejections reads the handler's per-token rejection counter, leaving out
// names with a count of 0.
func rejections(t *testing.T, h http.Handler) map[string]uint64 {
	t.Helper()
	c, ok := h.(rejectionCounter)
	if !ok {
		t.Fatalf("PublicHandler returned %T, which has no RateLimitRejections() map[string]uint64 for goldie.ratelimit.rejections{token}", h)
	}
	out := map[string]uint64{}
	for name, n := range c.RateLimitRejections() {
		if n != 0 {
			out[name] = n
		}
	}
	return out
}

const lookupPath = "/api/v1/articles/agent-loops"

// A token over its limit gets 429 in the error envelope, with Retry-After and
// a next-step hint, on every /api/ path, while a second token from the same
// address is unaffected: the limit is per token, not global and not per IP.
// The response conforms to the spec and leaks neither a secret nor an
// address.
func TestTokenOverItsLimitGets429WhileAnotherTokenIsUnaffected(t *testing.T) {
	srv := tokenServer(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "2/m")})
	for i := range 2 {
		if resp, _ := do(t, http.MethodGet, srv.URL+lookupPath, bearer(readerToken)); resp.StatusCode != http.StatusOK {
			t.Fatalf("reader request %d within the limit: status %d, want 200", i+1, resp.StatusCode)
		}
	}
	for _, path := range []string{lookupPath, "/api/v1/search?q=kubernetes", "/api/v1/articles/platform%2Fdeploy", "/api/v1/nope"} {
		resp, body := do(t, http.MethodGet, srv.URL+path, bearer(readerToken))
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("%s over the limit: status %d, want 429", path, resp.StatusCode)
			continue
		}
		code, hint := errorHint(t, body)
		if code != "rate_limited" {
			t.Errorf("%s: code %q, want rate_limited", path, code)
		}
		if !strings.Contains(hint, "Retry-After") {
			t.Errorf("%s: hint %q does not point to the Retry-After header", path, hint)
		}
		if got := resp.Header.Get("Retry-After"); got != "30" {
			t.Errorf("%s: Retry-After %q, want 30 (one request per 30 s, rounded up)", path, got)
		}
		for _, secret := range []string{readerToken, operatorToken, hashedSecret} {
			if strings.Contains(string(body), secret) {
				t.Errorf("%s: 429 body echoes a secret: %s", path, body)
			}
		}
		for name, re := range leaks {
			if m := re.FindString(string(body)); m != "" {
				t.Errorf("%s: 429 body contains a %s (%q): %s", path, name, m, body)
			}
		}
	}
	for _, token := range []string{operatorToken, hashedSecret} {
		for i := range 2 {
			if resp, _ := do(t, http.MethodGet, srv.URL+lookupPath, bearer(token)); resp.StatusCode != http.StatusOK {
				t.Errorf("token %.12s… request %d while the reader is limited: status %d, want 200", token, i+1, resp.StatusCode)
			}
		}
	}
}

// Requests within the limit pass, up to the burst; the request after it is
// the first rejection. A smaller burst than the rate holds too.
func TestRequestsWithinTheLimitPassUpToTheBurst(t *testing.T) {
	for limit, burst := range map[string]int{"3/m": 3, "2/m,burst=5": 5, "60/m,burst=1": 1, "1/s,burst=4": 4} {
		h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, limit)})
		for i := range burst {
			if rec := hit(t, h, lookupPath, readerToken); rec.Code != http.StatusOK {
				t.Fatalf("%s: request %d of a burst of %d: status %d, want 200", limit, i+1, burst, rec.Code)
			}
		}
		if rec := hit(t, h, lookupPath, readerToken); rec.Code != http.StatusTooManyRequests {
			t.Errorf("%s: request %d after a burst of %d: status %d, want 429", limit, burst+1, burst, rec.Code)
		}
	}
}

// Retry-After is the whole seconds until the token may send again, rounded
// up: the rate decides it, per second or per minute, and a wait under a
// second is 1, never 0.
func TestRetryAfterIsTheWaitInWholeSecondsRoundedUp(t *testing.T) {
	for limit, want := range map[string]string{"1/m": "60", "2/m,burst=1": "30", "3/m": "20", "1/s,burst=1": "1"} {
		h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, limit)})
		var rec *httptest.ResponseRecorder
		for range 10 {
			if rec = hit(t, h, lookupPath, readerToken); rec.Code == http.StatusTooManyRequests {
				break
			}
		}
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("%s: no 429 in 10 requests", limit)
			continue
		}
		if got := rec.Header().Get("Retry-After"); got != want {
			t.Errorf("%s: Retry-After %q, want %q", limit, got, want)
		}
	}
	h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "20/s,burst=1")})
	for range 200 {
		if rec := hit(t, h, lookupPath, readerToken); rec.Code == http.StatusTooManyRequests {
			if got := rec.Header().Get("Retry-After"); got != "1" {
				t.Errorf("20/s,burst=1: Retry-After %q, want 1 for a wait under a second", got)
			}
			return
		}
	}
	t.Error("20/s,burst=1: no 429 in 200 back-to-back requests")
}

// The bucket refills at the configured rate: at 1000/s with a burst of 1, a
// token that waits a few milliseconds between requests is never rejected.
func TestTheBucketRefillsAtTheConfiguredRate(t *testing.T) {
	h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "1000/s,burst=1")})
	for i := range 30 {
		if rec := hit(t, h, lookupPath, readerToken); rec.Code != http.StatusOK {
			t.Fatalf("request %d, 5 ms after the last: status %d, want 200 (the bucket must refill)", i+1, rec.Code)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := rejections(t, h)["ci-reader"]; got != 0 {
		t.Errorf("rejections = %d, want 0", got)
	}
}

// The default is generous: a burst of 40 back-to-back requests from one token
// passes, but the default is a limit, so a flood is rejected. The default is
// the one LoadConfig applies for an unset GOLDIE_RATE_LIMIT.
func TestDefaultLimitIsGenerousButBounded(t *testing.T) {
	h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "")})
	for i := range 40 {
		if rec := hit(t, h, lookupPath, readerToken); rec.Code != http.StatusOK {
			t.Fatalf("default limit: request %d of 40: status %d, want 200", i+1, rec.Code)
		}
	}
	limited := false
	for range 200 {
		if hit(t, h, lookupPath, readerToken).Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("default limit: 240 back-to-back requests were all served, want 429 once the burst is spent")
	}
	if rec := hit(t, h, lookupPath, operatorToken); rec.Code != http.StatusOK {
		t.Errorf("default limit: another token after the flood: status %d, want 200", rec.Code)
	}
}

// Unauthenticated requests are still 401, even while a token is limited, and
// they count against no token's bucket and no counter: a flood of missing,
// unknown and near-miss tokens leaves the reader its whole burst.
func TestUnauthenticatedRequestsAre401AndNotCounted(t *testing.T) {
	h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "1/m,burst=2")})
	strangers := map[string]string{
		"no token":         "",
		"unknown token":    "unknown-token-0123456789abcdef0123456789",
		"reader prefix":    readerToken[:len(readerToken)-1],
		"reader suffix":    readerToken + "x",
		"reader in caps":   strings.ToUpper(readerToken),
		"token name":       "ci-reader",
		"digest as token":  sha256Hex(hashedSecret),
		"whole token line": "ci-reader:reader:" + readerToken,
	}
	check401 := func(when string) {
		for who, token := range strangers {
			for range 5 {
				rec := hit(t, h, lookupPath, token)
				if rec.Code != http.StatusUnauthorized {
					t.Errorf("%s, %s: status %d, want 401", when, who, rec.Code)
					break
				}
				if code, _ := errorHint(t, rec.Body.Bytes()); code != "unauthorized" {
					t.Errorf("%s, %s: code %q, want unauthorized", when, who, code)
					break
				}
			}
		}
	}
	check401("before any token is limited")
	for i := range 2 {
		if rec := hit(t, h, lookupPath, readerToken); rec.Code != http.StatusOK {
			t.Fatalf("reader request %d after %d unauthenticated ones: status %d, want 200", i+1, 5*len(strangers), rec.Code)
		}
	}
	if rec := hit(t, h, lookupPath, readerToken); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("reader over its limit: status %d, want 429", rec.Code)
	}
	check401("while the reader is limited")
	got := rejections(t, h)
	if len(got) != 1 || got["ci-reader"] != 1 {
		t.Errorf("rejections = %v, want only ci-reader: 1", got)
	}
}

// Nothing the client controls besides the token moves it to another bucket:
// the scheme's case, forwarding headers and a fresh connection all leave a
// limited token limited.
func TestALimitedTokenCannotEscapeItsBucket(t *testing.T) {
	srv := tokenServer(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "1/m")})
	if resp, _ := do(t, http.MethodGet, srv.URL+lookupPath, bearer(readerToken)); resp.StatusCode != http.StatusOK {
		t.Fatalf("first request: status %d, want 200", resp.StatusCode)
	}
	for name, mutate := range map[string]func(*http.Request){
		"lower-case scheme": func(r *http.Request) { r.Header.Set("Authorization", "bearer "+readerToken) },
		"X-Forwarded-For": func(r *http.Request) {
			bearer(readerToken)(r)
			r.Header.Set("X-Forwarded-For", "203.0.113.7")
		},
		"X-Real-IP": func(r *http.Request) {
			bearer(readerToken)(r)
			r.Header.Set("X-Real-IP", "198.51.100.9")
		},
		"Forwarded": func(r *http.Request) {
			bearer(readerToken)(r)
			r.Header.Set("Forwarded", "for=192.0.2.60")
		},
		"new connection": func(r *http.Request) {
			bearer(readerToken)(r)
			r.Close = true
		},
	} {
		if resp, _ := do(t, http.MethodGet, srv.URL+lookupPath, mutate); resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("%s: status %d, want 429", name, resp.StatusCode)
		}
	}
}

// Concurrent requests from one token cannot overrun the bucket: of 40 at
// once against a burst of 5, exactly 5 pass and 35 are counted rejections.
func TestConcurrentRequestsCannotOverrunTheBurst(t *testing.T) {
	h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "1/m,burst=5")})
	const n = 40
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { codes[i] = hit(t, h, lookupPath, readerToken).Code })
	}
	wg.Wait()
	ok, limited := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusTooManyRequests:
			limited++
		}
	}
	if ok != 5 || limited != n-5 {
		t.Errorf("%d concurrent requests: %d passed and %d were 429, want 5 and %d", n, ok, limited, n-5)
	}
	if got := rejections(t, h)["ci-reader"]; got != n-5 {
		t.Errorf("rejections = %d, want %d", got, n-5)
	}
}

// The counter for goldie.ratelimit.rejections{token} counts each token's
// rejections under its name, plaintext, hashed and the GOLDIE_API_TOKEN
// shorthand alike, and never under a secret, a digest or an empty name. The
// map it returns is a copy.
func TestRejectionCounterIsPerTokenNameAndNeverASecret(t *testing.T) {
	h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "1/m")})
	if got := rejections(t, h); len(got) != 0 {
		t.Errorf("before any request: rejections = %v, want none", got)
	}
	for token, n := range map[string]int{readerToken: 4, hashedSecret: 2, operatorToken: 1} {
		for range n {
			hit(t, h, lookupPath, token)
		}
	}
	got := rejections(t, h)
	want := map[string]uint64{"ci-reader": 3, "ci-hashed": 1}
	if len(got) != len(want) || got["ci-reader"] != 3 || got["ci-hashed"] != 1 {
		t.Errorf("rejections = %v, want %v", got, want)
	}
	for name := range got {
		for _, secret := range []string{readerToken, operatorToken, hashedSecret, sha256Hex(hashedSecret), ""} {
			if name == secret || (secret != "" && strings.Contains(name, secret)) {
				t.Errorf("rejections are keyed by a secret or an empty name: %q", name)
			}
		}
	}
	raw := h.(rejectionCounter).RateLimitRejections()
	raw["ci-reader"] = 1000
	delete(raw, "ci-hashed")
	if again := rejections(t, h); again["ci-reader"] != 3 || again["ci-hashed"] != 1 {
		t.Errorf("changing the returned map changed the counter: %v", again)
	}

	short := rateHandler(t, platform.Config{APIToken: testToken, RateLimit: loadedRateLimit(t, "1/m")})
	for range 3 {
		hit(t, short, lookupPath, testToken)
	}
	got = rejections(t, short)
	if len(got) != 1 || got["api-token"] != 2 {
		t.Errorf("shorthand token: rejections = %v, want api-token: 2", got)
	}
}

// The buckets live in the handler, in process memory: a second handler, as
// after a restart, starts every token with its whole burst and its own
// counter.
func TestEachHandlerHoldsItsOwnBuckets(t *testing.T) {
	opts := platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "1/m")}
	first := rateHandler(t, opts)
	hit(t, first, lookupPath, readerToken)
	if rec := hit(t, first, lookupPath, readerToken); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("first handler, second request: status %d, want 429", rec.Code)
	}
	second := rateHandler(t, opts)
	if rec := hit(t, second, lookupPath, readerToken); rec.Code != http.StatusOK {
		t.Errorf("a new handler: status %d, want 200 with a full bucket", rec.Code)
	}
	if got := rejections(t, second); len(got) != 0 {
		t.Errorf("a new handler: rejections = %v, want none", got)
	}
}

// The OpenAPI documents are not behind the tokens, so a limited token can
// still read them.
func TestALimitedTokenCanStillReadTheSpec(t *testing.T) {
	h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), RateLimit: loadedRateLimit(t, "1/m")})
	hit(t, h, lookupPath, readerToken)
	for _, path := range []string{"/openapi.json", "/openapi.yaml"} {
		if rec := hit(t, h, path, readerToken); rec.Code != http.StatusOK {
			t.Errorf("%s with a limited token: status %d, want 200", path, rec.Code)
		}
	}
}

// PublicHandler applies the RateLimit it is given as is (#178): it parses
// nothing, so the limit LoadConfig read is the limit the handler enforces,
// and the burst decides when a token is first rejected. The one value it
// does not take as is, zero requests, it refuses rather than serving without
// a limit (#186): TestZeroValueRateLimitFailsClosed.
func TestPublicHandlerAppliesTheTypedLimitAsIs(t *testing.T) {
	for _, l := range []platform.RateLimit{
		{Requests: 1, Per: time.Minute, Burst: 2},
		{Requests: 3, Per: time.Second, Burst: 3},
		{Requests: 2, Per: time.Minute, Burst: 1},
	} {
		h := rateHandler(t, platform.Config{APITokens: namedTokenLines(), RateLimit: l})
		for i := range l.Burst {
			if rec := hit(t, h, lookupPath, readerToken); rec.Code != http.StatusOK {
				t.Fatalf("%+v: request %d of a burst of %d: status %d, want 200", l, i+1, l.Burst, rec.Code)
			}
		}
		if rec := hit(t, h, lookupPath, readerToken); rec.Code != http.StatusTooManyRequests {
			t.Errorf("%+v: request %d: status %d, want 429 once the burst is spent", l, l.Burst+1, rec.Code)
		}
	}
	if got, want := loadedRateLimit(t, "7/m,burst=3"), (platform.RateLimit{Requests: 7, Per: time.Minute, Burst: 3}); got != want {
		t.Errorf("LoadConfig handed PublicHandler %+v, want %+v", got, want)
	}
}

// The spec declares the 429 on every /api/ operation: the error envelope and
// an integer Retry-After header.
func TestSpecDeclaresTheRateLimitResponse(t *testing.T) {
	doc, err := contracttest.Spec()
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := doc["paths"].(map[string]any)
	n := 0
	for path, item := range paths {
		if !strings.HasPrefix(path, "/api/") {
			continue
		}
		ops, _ := item.(map[string]any)
		for method, op := range ops {
			o, ok := op.(map[string]any)
			if !ok || method == "parameters" {
				continue
			}
			n++
			responses, _ := o["responses"].(map[string]any)
			resp := contracttest.Resolve(doc, responses["429"])
			if resp == nil {
				t.Errorf("%s %s: no 429 response", method, path)
				continue
			}
			content, _ := resp["content"].(map[string]any)
			media, _ := content["application/json"].(map[string]any)
			schema, _ := media["schema"].(map[string]any)
			if schema["$ref"] != "#/components/schemas/ErrorEnvelope" {
				t.Errorf("%s %s: 429 body schema %v, want the ErrorEnvelope", method, path, schema)
			}
			headers, _ := resp["headers"].(map[string]any)
			header := contracttest.Resolve(doc, headers["Retry-After"])
			hs, _ := header["schema"].(map[string]any)
			if header == nil || hs["type"] != "integer" {
				t.Errorf("%s %s: 429 Retry-After header %v, want one with an integer schema", method, path, header)
			}
		}
	}
	if n == 0 {
		t.Fatal("the spec has no /api/ operations")
	}
}
