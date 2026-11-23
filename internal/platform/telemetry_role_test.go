package platform

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// Maintainer decision on goldie.mode (option a): the server's resource is
// goldie.role=server plus goldie.mode=all, the client roles carry
// goldie.role=cli|mcp and never goldie.mode, and OTEL_RESOURCE_ATTRIBUTES
// overrides neither key. These tests read the resource as the collector
// receives it.

// exportedResource sets telemetry up for role (server, cli or mcp) with the
// given OTEL_RESOURCE_ATTRIBUTES, sends one traced request, and returns the
// resource attributes of the exported trace.
func exportedResource(t *testing.T, role, resAttrs string) map[string]string {
	t.Helper()
	collector, seen := newCollector(t)
	env := map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL}
	if resAttrs != "" {
		env["OTEL_RESOURCE_ATTRIBUTES"] = resAttrs
	}
	var tel *Telemetry
	var err error
	if role == RoleServer {
		tel, err = SetupTelemetry(context.Background(), mapLookup(env))
	} else {
		tel, err = SetupClientTelemetry(context.Background(), mapLookup(env), role)
	}
	if err != nil || !tel.Enabled() {
		t.Fatalf("%s: enabled=%v err=%v", role, tel.Enabled(), err)
	}
	api := newAPIStub(t, nil)
	doAll(t, tel.WrapTransport(http.DefaultTransport), plantedRequests(t, api.srv.URL)[:1])
	shutdownWithin(t, tel, 5*time.Second)

	seen.mu.Lock()
	defer seen.mu.Unlock()
	body := seen.bodies["/v1/traces"]
	if len(body) == 0 {
		t.Fatalf("%s: no spans reached the collector", role)
	}
	res := map[string]string{}
	for _, rs := range decodeTraces(t, body).ResourceSpans {
		for _, kv := range rs.GetResource().GetAttributes() {
			res[kv.Key] = kv.Value.GetStringValue()
		}
	}
	return res
}

func TestServerResourceCarriesRoleServerAndModeAll(t *testing.T) {
	res := exportedResource(t, RoleServer, "")
	if res["goldie.role"] != "server" {
		t.Errorf("goldie.role = %q, want server", res["goldie.role"])
	}
	if res["goldie.mode"] != "all" {
		t.Errorf("goldie.mode = %q, want all", res["goldie.mode"])
	}
}

func TestClientResourceCarriesItsRoleAndNeverMode(t *testing.T) {
	for _, role := range []string{RoleCLI, RoleMCP} {
		t.Run(role, func(t *testing.T) {
			res := exportedResource(t, role, "")
			if res["goldie.role"] != role {
				t.Errorf("goldie.role = %q, want %q", res["goldie.role"], role)
			}
			if v, ok := res["goldie.mode"]; ok {
				t.Errorf("goldie.mode = %q on a client role, want it absent", v)
			}
		})
	}
}

func TestResourceAttributesCannotOverrideRoleOrMode(t *testing.T) {
	const planted = "team=a,goldie.role=x,goldie.mode=y"
	for _, tc := range []struct {
		role, mode string // mode "" means goldie.mode must be absent
	}{{RoleServer, "all"}, {RoleCLI, ""}, {RoleMCP, ""}} {
		t.Run(tc.role, func(t *testing.T) {
			res := exportedResource(t, tc.role, planted)
			if res["goldie.role"] != tc.role {
				t.Errorf("goldie.role = %q, want %q (OTEL_RESOURCE_ATTRIBUTES must not override it)", res["goldie.role"], tc.role)
			}
			got, ok := res["goldie.mode"]
			if tc.mode == "" && ok {
				t.Errorf("goldie.mode = %q planted onto a client role, want it absent", got)
			}
			if tc.mode != "" && got != tc.mode {
				t.Errorf("goldie.mode = %q, want %q", got, tc.mode)
			}
			if res["team"] != "a" {
				t.Errorf("team = %q, want the other resource attributes kept", res["team"])
			}
		})
	}
}
