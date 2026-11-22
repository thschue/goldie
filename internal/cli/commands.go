package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

func (c *commander) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.out, "%s\n", b)
	return err
}

func (c *commander) search(ctx context.Context, q string) error {
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	resp, err := cl.SearchArticlesWithResponse(ctx, &contract.SearchArticlesParams{Q: q})
	if err != nil {
		return unreachable()
	}
	if resp.JSON200 == nil {
		return apiError(resp.HTTPResponse, resp.Body)
	}
	if c.s.json {
		return c.printJSON(resp.JSON200)
	}
	var b strings.Builder
	hits := resp.JSON200.Results
	if len(hits) == 0 {
		fmt.Fprintf(&b, "No articles match %q.\n\n", clean(q))
	} else {
		noun := "articles"
		if len(hits) == 1 {
			noun = "article"
		}
		fmt.Fprintf(&b, "%d %s for %q, best first:\n\n", len(hits), noun, clean(q))
		for _, h := range hits {
			fmt.Fprintf(&b, "  %s  %s\n", clean(h.Id), clean(h.Name))
			fmt.Fprintf(&b, "    score %d, matched: %s\n", h.Score, cleanList(h.Matched))
			fmt.Fprintf(&b, "    keywords: %s\n", cleanList(h.Keywords))
		}
		b.WriteString("\nRead one with: goldie lookup <id>\n")
	}
	b.WriteString("Try other words: goldie search <words>\n")
	_, err = fmt.Fprint(c.out, b.String())
	return err
}

func (c *commander) lookup(ctx context.Context, id string) error {
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	resp, err := cl.GetArticleWithResponse(ctx, id)
	if err != nil {
		return unreachable()
	}
	if resp.JSON200 == nil {
		return apiError(resp.HTTPResponse, resp.Body)
	}
	if c.s.json {
		return c.printJSON(resp.JSON200)
	}
	a := resp.JSON200
	_, err = fmt.Fprintf(c.out, "%s\nid: %s\nkeywords: %s\n\n%s\n\nFind more with: goldie search <words>\n",
		clean(a.Name), clean(a.Id), cleanList(a.Keywords), strings.TrimRight(clean(a.Content), "\n"))
	return err
}

// status reports whether the server answers and whether it accepts the token.
// The API has no status or sync route yet (that is the sync work, #6), so this
// reports only what the contract supports: the API document and one search.
// The token goes only to the search, never to the public API document.
//
// TODO(#6): the token probe is a real search, so it will count as search and
// Miss activity once those are recorded. Replace it with the status route.
func (c *commander) status(ctx context.Context) error {
	cl, err := c.client(false)
	if err != nil {
		return err
	}
	doc, err := cl.GetOpenAPIJSONWithResponse(ctx)
	if err != nil {
		return unreachable()
	}
	if doc.JSON200 == nil {
		return apiError(doc.HTTPResponse, doc.Body)
	}
	title, apiVersion := specInfo(*doc.JSON200)
	auth := "no token given (set --token or " + EnvToken + " to check it)"
	accepted := false
	if c.s.token != "" {
		authed, err := c.client(true)
		if err != nil {
			return err
		}
		resp, err := authed.SearchArticlesWithResponse(ctx, &contract.SearchArticlesParams{Q: "goldie"})
		switch {
		case err != nil:
			return unreachable()
		case resp.JSON200 != nil:
			auth, accepted = "token accepted", true
		case resp.StatusCode() == http.StatusUnauthorized:
			auth = "token rejected"
		default:
			return apiError(resp.HTTPResponse, resp.Body)
		}
	}
	if c.s.json {
		return c.printJSON(map[string]any{"reachable": true, "api": title, "apiVersion": apiVersion, "tokenAccepted": accepted})
	}
	_, err = fmt.Fprintf(c.out, "Server: reachable\nAPI: %s %s\nAuth: %s\n\nSearch with: goldie search <words>\n",
		clean(title), clean(apiVersion), auth)
	return err
}

// version prints the client version, and the server's API version when an
// address is configured and the server answers. The server part is best
// effort: GOLDIE_ADDR is also the server's listen address (":8080"), so an
// address that is not a URL is skipped instead of failing the command.
func (c *commander) version(ctx context.Context) error {
	info := map[string]any{"version": platform.Version}
	line := platform.Version
	if cl, err := c.client(false); err == nil {
		if doc, err := cl.GetOpenAPIJSONWithResponse(ctx); err == nil && doc.JSON200 != nil {
			_, apiVersion := specInfo(*doc.JSON200)
			info["serverApiVersion"] = apiVersion
			line += "\nserver API: " + clean(apiVersion)
		}
	}
	if c.s.json {
		return c.printJSON(info)
	}
	_, err := fmt.Fprintln(c.out, line)
	return err
}

// specInfo reads the title and version from an OpenAPI document.
func specInfo(doc map[string]any) (title, version string) {
	info, _ := doc["info"].(map[string]any)
	title, _ = info["title"].(string)
	version, _ = info["version"].(string)
	return title, version
}

// unreachable words a transport failure. The cause is dropped: it can carry
// the address, and the user already knows what they typed.
func unreachable() error {
	return fmt.Errorf("cannot reach the server: check --addr or %s and that the server is running", EnvAddr)
}

// apiError turns an error response into an error that carries the envelope's
// message and hint. The server's text is untrusted, so it is cleaned.
func apiError(resp *http.Response, body []byte) error {
	status := http.StatusText(resp.StatusCode)
	var env contract.ErrorEnvelope
	if json.Unmarshal(body, &env) != nil || env.Error.Message == "" {
		return fmt.Errorf("the server answered %d %s", resp.StatusCode, status)
	}
	msg := fmt.Sprintf("%s (%d %s)", clean(env.Error.Message), resp.StatusCode, status)
	if h := clean(env.Error.Details.Hint); h != "" {
		msg += "\nHint: " + h
	}
	return errors.New(msg)
}

// clean removes control characters other than newline and tab, so content
// from a Source cannot drive the terminal with escape sequences.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f)) {
			return r
		}
		return -1
	}, s)
}

func cleanList(l []string) string {
	if len(l) == 0 {
		return "(none)"
	}
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = clean(s)
	}
	return strings.Join(out, ", ")
}
