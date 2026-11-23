package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// #327: a real goldie process exports the contract's route templates as
// url.path, never an id, a wrong-case path or an unknown path, whether it
// runs as goldie server or goldie server -dev. Both must wire the path
// function that internal/app provides into platform.

// traceCollector is an OTLP collector that keeps what it got on /v1/traces.
type traceCollector struct {
	*httptest.Server
	mu     sync.Mutex
	traces []byte
}

func newTraceCollector(t *testing.T) *traceCollector {
	t.Helper()
	c := &traceCollector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body = zr
		}
		b, _ := io.ReadAll(body)
		if r.URL.Path == "/v1/traces" {
			c.mu.Lock()
			c.traces = append(c.traces, b...)
			c.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *traceCollector) got() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.traces...)
}

// hostilePaths are requested with the token; none is a route except the
// first three, which are the article route in three spellings.
var hostilePaths = []string{
	"/api/v1/articles/id-marker-7731",
	"/API/V1/articles/id-marker-7731",
	"/api/v1/articles/id-marker-7731?q=query-marker-5512",
	"/zz-unknown-marker-9904/segment-marker-1187",
	"/api/v1/zz-unknown-marker-9904",
	"/zz-unknown-marker-9904/%00%1b%0d%0a",
	"/api/v1/articles/" + strings.Repeat("L", 5000),
}

func assertTemplatesOnly(t *testing.T, body []byte) {
	t.Helper()
	if !bytes.Contains(body, []byte("url.path")) {
		t.Fatalf("no span with url.path reached the collector (%d bytes)", len(body))
	}
	if !bytes.Contains(body, []byte("/api/v1/articles/{id}")) {
		t.Errorf("the exported spans do not carry the template /api/v1/articles/{id}")
	}
	for _, bad := range []string{
		"id-marker-7731", "query-marker-5512", "zz-unknown-marker-9904", "segment-marker-1187",
		"/API/V1", "/api/v1/articles/id", "LLLLLLLL", "\x1b",
	} {
		if bytes.Contains(body, []byte(bad)) {
			t.Errorf("the exported spans carry %q", bad)
		}
	}
}

func TestServerDevExportsContractTemplatesNotPaths(t *testing.T) {
	c := newTraceCollector(t)
	p := startDev(t, devEnv("OTEL_EXPORTER_OTLP_ENDPOINT="+c.URL, "OTEL_LOGS_EXPORTER=none", "OTEL_METRICS_EXPORTER=none"), contentDir(t))
	for _, path := range hostilePaths {
		devStatus(t, p.addr, path, p.token)
	}
	p.stop(t)
	assertTemplatesOnly(t, c.got())
}

func TestServerExportsContractTemplatesNotPaths(t *testing.T) {
	c := newTraceCollector(t)
	const token = "span-path-token-0123456789abcdef0123456789"
	p := launchDev(t, "", devEnv(
		"OTEL_EXPORTER_OTLP_ENDPOINT="+c.URL, "OTEL_LOGS_EXPORTER=none", "OTEL_METRICS_EXPORTER=none",
		"GOLDIE_CONTENT_DIR="+contentDir(t), "GOLDIE_API_TOKEN="+token,
		"GOLDIE_ADDR=127.0.0.1:0", "GOLDIE_ADMIN_ADDR=127.0.0.1:0",
	), "server")
	deadline := time.After(10 * time.Second)
	for p.app == "" {
		p.scan()
		if p.app != "" {
			break
		}
		select {
		case <-p.changed:
		case err := <-p.exited:
			t.Fatalf("goldie server exited early: %v\n%s", err, p.dump())
		case <-deadline:
			t.Fatalf("goldie server did not start listening:\n%s", p.dump())
		}
	}
	for _, path := range hostilePaths {
		devStatus(t, "http://"+p.app, path, token)
	}
	p.stop(t)
	assertTemplatesOnly(t, c.got())
}
