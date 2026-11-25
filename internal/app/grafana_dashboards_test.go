package app

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/encoding/protowire"
)

// Acceptance tests for #899 (slice of #31): the repository ships a Grafana
// dashboard (JSON) with data links to the UI and Prometheus alert rules, both
// documented. The seam is the files and the metrics a running server exports:
// every metric, and every label, that the dashboard and the alert rules query
// must be one goldie emits, so a renamed metric breaks the build and not the
// dashboard. The dashboard is the one JSON file in the repository with a
// top-level panels array, the alert rules the one YAML file with
// groups[].rules[].alert (web, node_modules, testdata and .claude are not
// searched); the issue names no path.

const dashRoot = "../.."

// dashFiles finds the dashboard JSON files and the alert rule YAML files.
func dashFiles(t *testing.T) (dashboards, alerts []string) {
	t.Helper()
	skip := map[string]bool{".git": true, "node_modules": true, "web": true, "testdata": true, ".claude": true}
	err := filepath.WalkDir(dashRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] && path != dashRoot {
				return fs.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".json":
			var doc struct {
				Panels []json.RawMessage `json:"panels"`
			}
			if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &doc) == nil && len(doc.Panels) > 0 {
				dashboards = append(dashboards, path)
			}
		case ".yaml", ".yml":
			if len(dashAlertRules(t, path)) > 0 {
				alerts = append(alerts, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return dashboards, alerts
}

type dashAlert struct {
	Alert       string            `yaml:"alert"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

func dashAlertRules(t *testing.T, path string) []dashAlert {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Groups []struct {
			Rules []dashAlert `yaml:"rules"`
		} `yaml:"groups"`
	}
	if yaml.Unmarshal(b, &doc) != nil {
		return nil
	}
	var rules []dashAlert
	for _, g := range doc.Groups {
		for _, r := range g.Rules {
			if r.Alert != "" {
				rules = append(rules, r)
			}
		}
	}
	return rules
}

func dashOnly(t *testing.T, what string, files []string) string {
	t.Helper()
	if len(files) != 1 {
		t.Fatalf("want exactly one %s file in the repository, found %v", what, files)
	}
	return files[0]
}

// dashJSON decodes the dashboard.
func dashJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%s is not JSON: %v", path, err)
	}
	return doc
}

// dashWalk calls f with every key and value of the JSON document v.
func dashWalk(v any, f func(key string, val any)) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			f(k, val)
			dashWalk(val, f)
		}
	case []any:
		for _, val := range x {
			dashWalk(val, f)
		}
	}
}

// dashExprs are the Prometheus expressions of every panel target.
func dashExprs(doc map[string]any) []string {
	var exprs []string
	dashWalk(doc, func(k string, v any) {
		if s, ok := v.(string); ok && k == "expr" {
			exprs = append(exprs, s)
		}
	})
	return exprs
}

// dashQueried returns the goldie_ metric names in expr, and the label names it
// matches or groups by.
var (
	dashMetricRE = regexp.MustCompile(`\bgoldie_[a-z0-9_]+`)
	dashSelector = regexp.MustCompile(`goldie_[a-z0-9_]+\{([^}]*)\}`)
	dashLabelRE  = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:=~|!~|!=|=)`)
	dashByRE     = regexp.MustCompile(`(?:by|without)\s*\(([^)]*)\)`)
)

func dashQueried(expr string) (metrics, labels []string) {
	metrics = dashMetricRE.FindAllString(expr, -1)
	for _, m := range dashSelector.FindAllStringSubmatch(expr, -1) {
		for _, l := range dashLabelRE.FindAllStringSubmatch(m[1], -1) {
			labels = append(labels, l[1])
		}
	}
	for _, m := range dashByRE.FindAllStringSubmatch(expr, -1) {
		for _, l := range strings.Split(m[1], ",") {
			if l = strings.TrimSpace(l); l != "" {
				labels = append(labels, l)
			}
		}
	}
	return metrics, labels
}

// dashEmitted reads from OTLP exports the attribute keys of every metric, by
// OpenTelemetry name.
func dashEmitted(t *testing.T, exports [][]byte) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	attrKey := func(b []byte) string {
		var key string
		pbFields(t, b, func(num protowire.Number, _ uint64, raw []byte) {
			if num == 1 {
				key = string(raw)
			}
		})
		return key
	}
	metric := func(b []byte) {
		var name string
		var keys []string
		pbFields(t, b, func(num protowire.Number, _ uint64, raw []byte) {
			switch num {
			case 1:
				name = string(raw)
			case 5, 7, 9: // gauge, sum, histogram
				attrs := protowire.Number(7) // a number point; a histogram point has them at 9
				if num == 9 {
					attrs = 9
				}
				pbFields(t, raw, func(num protowire.Number, _ uint64, point []byte) {
					if num != 1 {
						return
					}
					pbFields(t, point, func(num protowire.Number, _ uint64, kv []byte) {
						if num == attrs {
							keys = append(keys, attrKey(kv))
						}
					})
				})
			}
		})
		if name != "" {
			for _, k := range keys {
				if !slices.Contains(out[name], k) {
					out[name] = append(out[name], k)
				}
			}
			if _, ok := out[name]; !ok {
				out[name] = nil
			}
		}
	}
	for _, export := range exports {
		pbFields(t, export, func(num protowire.Number, _ uint64, rm []byte) {
			if num != 1 {
				return
			}
			pbFields(t, rm, func(num protowire.Number, _ uint64, sm []byte) {
				if num != 2 {
					return
				}
				pbFields(t, sm, func(num protowire.Number, _ uint64, m []byte) {
					if num == 2 {
						metric(m)
					}
				})
			})
		})
	}
	return out
}

// dashEmittedByQueryName maps the Prometheus names a metric is queried under
// (suffixes _total, _seconds, _bucket, _sum and _count) to its attribute keys.
func dashEmittedByQueryName(emitted map[string][]string) map[string][]string {
	byName := map[string][]string{}
	for name, keys := range emitted {
		base := strings.ReplaceAll(name, ".", "_")
		for _, suffix := range []string{"", "_total", "_seconds", "_seconds_bucket", "_seconds_sum", "_seconds_count", "_bucket", "_sum", "_count"} {
			byName[base+suffix] = keys
		}
	}
	return byName
}

// dashExporting starts a git-backed server that exports metrics and serves one
// of everything every instrument counts: a search, a lookup, a skill get, an
// item use, an MCP tool call, a sync and the Snapshot's items. It returns the
// metrics of every export once all of them have arrived.
func dashExporting(t *testing.T) map[string][]string {
	t.Helper()
	repo := newBareRepo(t)
	repo.commit(gaugeFiles(map[string]string{"global/skills/deploy/SKILL.md": "# Deploy\n"}))
	log := newExportLog(t)
	pub, _ := gaugeServer(t, repo, log)
	srv := httptest.NewServer(pub)
	t.Cleanup(srv.Close)
	for _, path := range []string{"/api/v1/search?q=x", "/api/v1/articles/a", "/api/v1/skills/deploy"} {
		eventually(t, 10*time.Second, "GET "+path+" never answered 200", func() bool {
			resp, _ := do(t, http.MethodGet, srv.URL+path, bearer(testToken))
			return resp.StatusCode == http.StatusOK
		})
	}
	cs := mustConnectHTTP(t, srv.URL+mcpEndpoint, withBearer(testToken, http.DefaultTransport))
	if text, isErr := call(t, cs, "get_article", map[string]any{"id": "a"}); isErr {
		t.Fatalf("get_article: %s", text)
	}
	want := []string{"goldie.searches", "goldie.lookups", "goldie.skill.gets", "goldie.item.uses", "goldie.mcp.tool.calls", "goldie.syncs", "goldie.sync.duration", "goldie.items"}
	var emitted map[string][]string
	eventually(t, 15*time.Second, "not every goldie metric was exported", func() bool {
		emitted = dashEmitted(t, log.exports())
		for _, name := range want {
			if _, ok := emitted[name]; !ok {
				return false
			}
		}
		return true
	})
	return emitted
}

// wantQueriesEmitted fails for each metric or label in exprs that goldie does
// not emit. Labels the Prometheus scrape or OTLP resource adds are allowed.
func wantQueriesEmitted(t *testing.T, where string, exprs []string, emitted map[string][]string) {
	t.Helper()
	byName := dashEmittedByQueryName(emitted)
	added := []string{"le", "job", "instance", "service_name", "service_version", "service_instance_id", "goldie_role", "goldie_mode", "otel_scope_name", "otel_scope_version", "namespace", "pod", "container", "cluster"}
	for _, expr := range exprs {
		metrics, labels := dashQueried(expr)
		if len(metrics) == 0 {
			t.Errorf("%s: %q queries no goldie metric", where, expr)
		}
		allowed := slices.Clone(added)
		for _, m := range metrics {
			keys, ok := byName[m]
			if !ok {
				t.Errorf("%s: %q queries %s, which goldie does not emit", where, expr, m)
			}
			allowed = append(allowed, keys...)
		}
		for _, l := range labels {
			if !slices.Contains(allowed, l) {
				t.Errorf("%s: %q uses label %q, which none of its metrics carries (%v)", where, expr, l, allowed)
			}
		}
	}
}

// Criterion: every metric queried by the dashboard JSON is emitted, with the
// attributes the dashboard uses, and the dashboard shows the Item uses of #898.
func TestGrafanaDashboardQueriesOnlyMetricsGoldieEmits(t *testing.T) {
	dashboards, _ := dashFiles(t)
	path := dashOnly(t, "dashboard JSON", dashboards)
	exprs := dashExprs(dashJSON(t, path))
	if len(exprs) == 0 {
		t.Fatalf("%s has no panel with a Prometheus expr", path)
	}
	wantQueriesEmitted(t, path, exprs, dashExporting(t))
	if !strings.Contains(strings.Join(exprs, "\n"), "goldie_item_uses") {
		t.Errorf("%s queries no goldie_item_uses, the metric the dashboard exists for", path)
	}
}

// Criterion: the dashboard JSON has data links to the UI. Each link has a title
// and an http(s), templated or absolute-path URL; none runs script.
func TestGrafanaDashboardHasDataLinksToTheUI(t *testing.T) {
	dashboards, _ := dashFiles(t)
	doc := dashJSON(t, dashOnly(t, "dashboard JSON", dashboards))
	var urls []string
	dashWalk(doc, func(k string, v any) {
		if k != "links" {
			return
		}
		list, _ := v.([]any)
		for _, l := range list {
			link, _ := l.(map[string]any)
			url, _ := link["url"].(string)
			title, _ := link["title"].(string)
			if url == "" || title == "" {
				t.Errorf("a dashboard link needs a title and a url, got %v", link)
				continue
			}
			urls = append(urls, url)
		}
	})
	if len(urls) == 0 {
		t.Fatal("the dashboard has no data link to the UI")
	}
	for _, u := range urls {
		lower := strings.ToLower(strings.TrimSpace(u))
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "${") && !strings.HasPrefix(lower, "/") {
			t.Errorf("data link %q is not an http(s), templated or absolute-path URL", u)
		}
	}
}

// Criterion: the alert rules query only metrics goldie emits, and each alert
// has a duration, a severity and a summary.
func TestGrafanaAlertRulesQueryOnlyMetricsGoldieEmits(t *testing.T) {
	_, alerts := dashFiles(t)
	path := dashOnly(t, "alert rules YAML", alerts)
	rules := dashAlertRules(t, path)
	var exprs []string
	for _, r := range rules {
		exprs = append(exprs, r.Expr)
		if r.For == "" || r.Labels["severity"] == "" || r.Annotations["summary"] == "" {
			t.Errorf("alert %s needs for, a severity label and a summary annotation, got %+v", r.Alert, r)
		}
	}
	wantQueriesEmitted(t, path, exprs, dashExporting(t))
}

// Criterion: the dashboard and the alert rules are documented. A page under
// docs/ names both files by path, and every alert by name.
func TestDashboardAndAlertRulesAreDocumented(t *testing.T) {
	dashboards, alerts := dashFiles(t)
	dash, rules := dashOnly(t, "dashboard JSON", dashboards), dashOnly(t, "alert rules YAML", alerts)
	var docs strings.Builder
	err := filepath.WalkDir(filepath.Join(dashRoot, "docs"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".md") {
			if b, err := os.ReadFile(path); err == nil {
				docs.Write(b)
				docs.WriteString("\n")
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	text := docs.String()
	for _, file := range []string{dash, rules} {
		rel, _ := filepath.Rel(dashRoot, file)
		if !strings.Contains(text, filepath.ToSlash(rel)) {
			t.Errorf("docs/ does not mention %s by path", rel)
		}
	}
	for _, r := range dashAlertRules(t, rules) {
		if !strings.Contains(text, r.Alert) {
			t.Errorf("docs/ does not document the alert %s", r.Alert)
		}
	}
}
