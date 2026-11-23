package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// Acceptance tests for #494 (slice 7 of #21): SearchHit and Article carry the
// additive fields overrides (the Scopes the winning Item shadows, [] when none,
// never null) and enforced (boolean), so a client shows them without computing
// its own view. Values come from the Resolution view of #493, over the
// testdata/items-content fixture:
//   - the shop copy of platform/deploy shadows the global one: overrides
//     ["global"], not enforced;
//   - global platform/policy is enforced and shadows nothing;
//   - shop-rule is enforced in the Project and shadows nothing;
//   - global-only shadows nothing.
//
// The names are snake_case (ADR 0024).

const overridesShop = "github.com/acme/shop"

type overridesFields struct {
	ID        string    `json:"id"`
	Overrides *[]string `json:"overrides"`
	Enforced  *bool     `json:"enforced"`
}

func searchFieldsRaw(t *testing.T, q, project string) ([]overridesFields, []byte) {
	t.Helper()
	v := url.Values{"q": {q}}
	if project != "" {
		v.Set("project", project)
	}
	resp, body := apiGet(t, itemsServer(t).URL+"/api/v1/search?"+v.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search %q: status %d: %s", q, resp.StatusCode, body)
	}
	var res struct {
		Results []overridesFields `json:"results"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("search body %q is not JSON: %v", body, err)
	}
	return res.Results, body
}

func hitByID(t *testing.T, hits []overridesFields, id string) overridesFields {
	t.Helper()
	for _, h := range hits {
		if h.ID == id {
			return h
		}
	}
	t.Fatalf("no hit %q in %+v", id, hits)
	return overridesFields{}
}

func articleFields(t *testing.T, id, project string) (overridesFields, []byte) {
	t.Helper()
	path := "/api/v1/articles/" + url.PathEscape(id)
	if project != "" {
		path += "?" + url.Values{"project": {project}}.Encode()
	}
	resp, body := apiGet(t, itemsServer(t).URL+path)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, resp.StatusCode, body)
	}
	var a overridesFields
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("article body %q is not JSON: %v", body, err)
	}
	return a, body
}

func wantFields(t *testing.T, what string, got overridesFields, overrides []string, enforced bool) {
	t.Helper()
	if got.Overrides == nil {
		t.Errorf("%s: overrides is absent or null, want %q", what, overrides)
	} else if !slices.Equal(*got.Overrides, overrides) {
		t.Errorf("%s: overrides = %q, want %q", what, *got.Overrides, overrides)
	}
	if got.Enforced == nil {
		t.Errorf("%s: enforced is absent, want %v", what, enforced)
	} else if *got.Enforced != enforced {
		t.Errorf("%s: enforced = %v, want %v", what, *got.Enforced, enforced)
	}
}

func TestSearchHitsSayWhichScopesTheyOverride(t *testing.T) {
	hits, _ := searchFieldsRaw(t, "deploy", overridesShop)
	wantFields(t, "shop platform/deploy", hitByID(t, hits, "platform/deploy"), []string{"global"}, false)
}

func TestSearchHitsThatShadowNothingHaveAnEmptyOverridesListNotNull(t *testing.T) {
	hits, body := searchFieldsRaw(t, "global", overridesShop)
	wantFields(t, "global-only", hitByID(t, hits, "global-only"), []string{}, false)
	if !strings.Contains(string(body), `"overrides":[]`) {
		t.Errorf("body %s does not carry overrides as the JSON empty list", body)
	}
	if strings.Contains(string(body), `"overrides":null`) {
		t.Errorf("body %s carries overrides:null", body)
	}
}

func TestSearchHitsSayWhenAnItemIsEnforced(t *testing.T) {
	hits, _ := searchFieldsRaw(t, "policy", overridesShop)
	wantFields(t, "global platform/policy", hitByID(t, hits, "platform/policy"), []string{}, true)

	hits, _ = searchFieldsRaw(t, "rule", overridesShop)
	wantFields(t, "shop-rule", hitByID(t, hits, "shop-rule"), []string{}, true)
}

func TestSearchHitsWithoutAProjectOverrideNothing(t *testing.T) {
	hits, _ := searchFieldsRaw(t, "deploy", "")
	wantFields(t, "global platform/deploy", hitByID(t, hits, "platform/deploy"), []string{}, false)
}

func TestSearchHitsForAnotherProjectDoNotLeakTheShopOverride(t *testing.T) {
	hits, _ := searchFieldsRaw(t, "deploy", "github.com/acme/plain")
	wantFields(t, "plain view platform/deploy", hitByID(t, hits, "platform/deploy"), []string{}, false)
}

func TestSearchHitsOfADeprecatedItemStillCarryBothFields(t *testing.T) {
	hits, _ := searchFieldsRaw(t, "old", overridesShop)
	wantFields(t, "old-deploy", hitByID(t, hits, "old-deploy"), []string{}, false)
}

func TestArticleSaysWhichScopesItOverrides(t *testing.T) {
	a, _ := articleFields(t, "platform/deploy", overridesShop)
	wantFields(t, "shop platform/deploy", a, []string{"global"}, false)
}

func TestArticleThatShadowsNothingHasAnEmptyOverridesListNotNull(t *testing.T) {
	a, body := articleFields(t, "runbook", overridesShop)
	wantFields(t, "runbook", a, []string{}, false)
	if !strings.Contains(string(body), `"overrides":[]`) || strings.Contains(string(body), `"overrides":null`) {
		t.Errorf("body %s must carry overrides as [] and never null", body)
	}
}

func TestArticleSaysWhenItIsEnforced(t *testing.T) {
	a, _ := articleFields(t, "platform/policy", overridesShop)
	wantFields(t, "global platform/policy", a, []string{}, true)
	a, _ = articleFields(t, "shop-rule", overridesShop)
	wantFields(t, "shop-rule", a, []string{}, true)
}

func TestArticleWithoutAProjectOverridesNothing(t *testing.T) {
	a, _ := articleFields(t, "platform/deploy", "")
	wantFields(t, "global platform/deploy", a, []string{}, false)
}

// The spec declares both fields on SearchHit and Article, snake_case, with the
// right types, and keeps the properties that were there (./run
// contract-compat covers the full diff; this fixes the new shape).
func TestSpecDeclaresOverridesAndEnforcedOnSearchHitsAndArticles(t *testing.T) {
	resp, body := apiGet(t, itemsServer(t).URL+"/openapi.json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SearchHit", "Article"} {
		props := schemaProps(doc, map[string]any{"$ref": "#/components/schemas/" + name})
		ov, _ := props["overrides"].(map[string]any)
		if ov == nil {
			t.Errorf("%s has no overrides property", name)
		} else {
			ov = schemaOf(doc, ov)
			if ov["type"] != "array" {
				t.Errorf("%s.overrides type = %v, want array", name, ov["type"])
			}
			if it := schemaOf(doc, ov["items"]); it["type"] != "string" {
				t.Errorf("%s.overrides items type = %v, want string", name, it["type"])
			}
			if ov["nullable"] == true {
				t.Errorf("%s.overrides is nullable, want never null", name)
			}
		}
		en, _ := props["enforced"].(map[string]any)
		if en == nil {
			t.Errorf("%s has no enforced property", name)
		} else if schemaOf(doc, en)["type"] != "boolean" {
			t.Errorf("%s.enforced type = %v, want boolean", name, schemaOf(doc, en)["type"])
		}
		for _, old := range []string{"id", "name", "keywords"} {
			if _, ok := props[old]; !ok {
				t.Errorf("%s lost its %s property", name, old)
			}
		}
	}
}
