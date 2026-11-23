package platform

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// recordingTransport answers 200 and keeps the request it was given.
type recordingTransport struct{ got *http.Request }

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.got = req
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}, Request: req}, nil
}

// A request built by hand has a nil Header (http.Client fills it, a direct
// RoundTrip call does not). The wrapped transport must not panic on it, must
// still send traceparent, and must still export the client span.
func TestWrapTransportHandlesARequestWithANilHeader(t *testing.T) {
	spans := newSpanSink()
	client := newTelemetry(telemetryResource("", nil), defaultTraceSettings(), spans, nil)
	fake := &recordingTransport{}
	rt := client.WrapTransport(fake)

	u, err := url.Parse("http://goldie.invalid/api/v1/search")
	if err != nil {
		t.Fatal(err)
	}
	req := (&http.Request{Method: http.MethodGet, URL: u}).WithContext(context.Background())
	if req.Header != nil {
		t.Fatal("test setup: the request must have a nil Header")
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("RoundTrip panicked on a request with a nil Header: %v", r)
			}
		}()
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	shutdownWithin(t, client, 5*time.Second)

	if fake.got == nil {
		t.Fatal("the request never reached the transport")
	}
	if fake.got.Method != http.MethodGet || fake.got.URL.String() != u.String() {
		t.Errorf("the request on the wire is %s %s, want GET %s", fake.got.Method, fake.got.URL, u)
	}
	if tp := fake.got.Header.Get("Traceparent"); !strings.HasPrefix(tp, "00-") {
		t.Errorf("traceparent on the wire = %q, want a W3C traceparent", tp)
	}
	if n := len(spans.spans()); n != 1 {
		t.Errorf("got %d client spans, want 1", n)
	}
}
