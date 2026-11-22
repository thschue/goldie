package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// printJSON prints v as indented JSON. encoding/json escapes C0 controls but
// passes DEL, C1 and bidi controls through, so those are escaped too; they can
// only occur inside strings, where \uXXXX keeps the value.
func (c *commander) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	var out strings.Builder
	for _, r := range string(b) {
		if r >= 0x7f && unsafe(r) {
			fmt.Fprintf(&out, `\u%04x`, r)
		} else {
			out.WriteRune(r)
		}
	}
	_, err = fmt.Fprintf(c.out, "%s\n", out.String())
	return err
}

func (c *commander) search(ctx context.Context, q string) error {
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	resp, err := cl.SearchArticlesWithResponse(ctx, &contract.SearchArticlesParams{Q: q})
	if err != nil {
		return transportError(err)
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
		fmt.Fprintf(&b, "No articles match %q.\n\n", cleanLine(q))
	} else {
		noun := "articles"
		if len(hits) == 1 {
			noun = "article"
		}
		fmt.Fprintf(&b, "%d %s for %q, best first:\n\n", len(hits), noun, cleanLine(q))
		for _, h := range hits {
			fmt.Fprintf(&b, "  %s  %s\n", cleanLine(h.Id), cleanLine(h.Name))
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
	resp, err := cl.GetArticleWithResponse(ctx, id, nil)
	if err != nil {
		return transportError(err)
	}
	if resp.JSON200 == nil {
		return apiError(resp.HTTPResponse, resp.Body)
	}
	if c.s.json {
		return c.printJSON(resp.JSON200)
	}
	a := resp.JSON200
	_, err = fmt.Fprintf(c.out, "%s\nid: %s\nkeywords: %s\n\n%s\n\nFind more with: goldie search <words>\n",
		cleanLine(a.Name), cleanLine(a.Id), cleanList(a.Keywords), strings.TrimRight(clean(a.Content), "\n"))
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
		return transportError(err)
	}
	if doc.JSON200 == nil {
		return apiError(doc.HTTPResponse, doc.Body)
	}
	title, apiVersion := specInfo(*doc.JSON200)
	project, err := c.project()
	if err != nil {
		return err
	}
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
			return transportError(err)
		case resp.JSON200 != nil:
			auth, accepted = "token accepted", true
		case resp.StatusCode() == http.StatusUnauthorized:
			auth = "token rejected"
		default:
			return apiError(resp.HTTPResponse, resp.Body)
		}
	}
	if c.s.json {
		out := map[string]any{"reachable": true, "api": title, "apiVersion": apiVersion,
			"tokenGiven": c.s.token != "", "tokenAccepted": accepted, "project": nil, "projectSource": nil}
		if project.ID != "" {
			out["project"], out["projectSource"] = project.ID, project.Source
		}
		return c.printJSON(out)
	}
	_, err = fmt.Fprintf(c.out, "Server: reachable\nAPI: %s %s\nAuth: %s\nProject: %s\n\nSearch with: goldie search <words>\n",
		cleanLine(title), cleanLine(apiVersion), auth, project.describe())
	return err
}

// project resolves the Project for the working directory.
func (c *commander) project() (detectedProject, error) {
	dir, err := os.Getwd()
	if err != nil {
		return detectedProject{}, errors.New("cannot read the working directory")
	}
	envValue, _ := c.env.Lookup(EnvProject)
	return resolveProject(dir, c.s.project, envValue)
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
			line += "\nserver API: " + cleanLine(apiVersion)
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

// transportError words a transport failure. The cause is dropped: it can
// carry the address, and the user already knows what they typed.
func transportError(err error) error {
	if errors.Is(err, errTooLarge) {
		return fmt.Errorf("the server's answer is larger than %d MiB", maxResponseBytes>>20)
	}
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
	msg := fmt.Sprintf("%s (%d %s)", cleanLine(env.Error.Message), resp.StatusCode, status)
	if h := cleanLine(env.Error.Details.Hint); h != "" {
		msg += "\nHint: " + h
	}
	return errors.New(msg)
}

// unsafe reports the runes that can drive or spoof a terminal: C0 and C1
// controls, DEL and the bidirectional formatting characters.
func unsafe(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// clean removes unsafe runes other than newline and tab, so content from a
// Source cannot drive the terminal with escape sequences. It is for text that
// may span lines, such as an article's content.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unsafe(r) {
			return r
		}
		return -1
	}, s)
}

// cleanLine is clean for a single-line field: line breaks and tabs become
// spaces, so server text cannot forge extra output lines.
func cleanLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n', r == '\t', r == 0x2028, r == 0x2029:
			return ' '
		case unsafe(r):
			return -1
		}
		return r
	}, s)
}

func cleanList(l []string) string {
	if len(l) == 0 {
		return "(none)"
	}
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = cleanLine(s)
	}
	return strings.Join(out, ", ")
}
