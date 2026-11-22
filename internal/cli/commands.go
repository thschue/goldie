package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode/utf16"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// printJSON prints v as indented JSON. encoding/json escapes C0 controls but
// passes DEL, C1 and format characters through, so those are escaped too;
// they can only occur inside strings, where \uXXXX keeps the value. A rune
// outside the Basic Multilingual Plane, such as a tag character, is written
// as its UTF-16 surrogate pair, as JSON requires.
func (c *commander) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	var out strings.Builder
	for _, r := range string(b) {
		switch {
		case r < 0x7f || !contract.UnsafeRune(r):
			out.WriteRune(r)
		case r > 0xffff:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&out, `\u%04x\u%04x`, hi, lo)
		default:
			fmt.Fprintf(&out, `\u%04x`, r)
		}
	}
	_, err = fmt.Fprintf(c.out, "%s\n", out.String())
	return err
}

func (c *commander) search(ctx context.Context, q string) error {
	project, err := c.project()
	if err != nil {
		return err
	}
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	raw, err := cl.SearchArticles(ctx, &contract.SearchArticlesParams{Q: q, Project: project.param()})
	if err != nil {
		return transportError(err)
	}
	resp, err := parseOK(raw, contract.ParseSearchArticlesResponse)
	if err != nil {
		return err
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
		fmt.Fprintf(&b, "No articles match %q.\n\n", contract.CleanLine(q))
	} else {
		noun := "articles"
		if len(hits) == 1 {
			noun = "article"
		}
		fmt.Fprintf(&b, "%d %s for %q, best first:\n\n", len(hits), noun, contract.CleanLine(q))
		for _, h := range hits {
			fmt.Fprintf(&b, "  %s  %s\n", contract.CleanLine(h.Id), contract.CleanLine(h.Name))
			fmt.Fprintf(&b, "    score %d, matched: %s\n", h.Score, cleanList(h.Matched))
			fmt.Fprintf(&b, "    keywords: %s\n", cleanList(h.Keywords))
		}
		b.WriteString("\nRead one with: goldie lookup <id>\n")
	}
	b.WriteString("Try other words: goldie search <words>\n")
	writeHint(&b, resp.JSON200.Hint)
	_, err = fmt.Fprint(c.out, b.String())
	return err
}

func (c *commander) lookup(ctx context.Context, id string) error {
	project, err := c.project()
	if err != nil {
		return err
	}
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	raw, err := cl.GetArticle(ctx, id, &contract.GetArticleParams{Project: project.param()})
	if err != nil {
		return transportError(err)
	}
	resp, err := parseOK(raw, contract.ParseGetArticleResponse)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.HTTPResponse, resp.Body)
	}
	if c.s.json {
		return c.printJSON(resp.JSON200)
	}
	a := resp.JSON200
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nid: %s\nkeywords: %s\n\n%s\n\nFind more with: goldie search <words>\n",
		contract.CleanLine(a.Name), contract.CleanLine(a.Id), cleanList(a.Keywords), strings.TrimRight(contract.CleanText(a.Content), "\n"))
	writeHint(&b, a.Hint)
	_, err = fmt.Fprint(c.out, b.String())
	return err
}

// writeHint adds the server's hint for an unregistered Project, if any.
func writeHint(b *strings.Builder, hint *string) {
	if hint != nil && *hint != "" {
		fmt.Fprintf(b, "Hint: %s\n", contract.CleanLine(*hint))
	}
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
	var hint *string
	if c.s.token != "" {
		authed, err := c.client(true)
		if err != nil {
			return err
		}
		raw, err := authed.SearchArticles(ctx, &contract.SearchArticlesParams{Q: "goldie", Project: project.param()})
		if err != nil {
			return transportError(err)
		}
		if raw.StatusCode == http.StatusUnauthorized {
			_ = raw.Body.Close()
			auth = "token rejected"
		} else {
			resp, err := parseOK(raw, contract.ParseSearchArticlesResponse)
			switch {
			case err != nil:
				return err
			case resp.JSON200 == nil:
				return apiError(resp.HTTPResponse, resp.Body)
			}
			auth, accepted, hint = "token accepted", true, resp.JSON200.Hint
		}
	}
	if c.s.json {
		out := map[string]any{"reachable": true, "api": title, "apiVersion": apiVersion,
			"tokenGiven": c.s.token != "", "tokenAccepted": accepted, "project": nil, "projectSource": nil}
		if project.ID != "" {
			out["project"], out["projectSource"] = project.ID, project.Source
		}
		if hint != nil && *hint != "" {
			out["hint"] = contract.CleanLine(*hint)
		}
		return c.printJSON(out)
	}
	_, err = fmt.Fprintf(c.out, "Server: reachable\nAPI: %s %s\nAuth: %s\nProject: %s\n\nSearch with: goldie search <words>\n",
		contract.CleanLine(title), contract.CleanLine(apiVersion), auth, project.describe())
	if err == nil {
		var b strings.Builder
		writeHint(&b, hint)
		_, err = fmt.Fprint(c.out, b.String())
	}
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
			line += "\nserver API: " + contract.CleanLine(apiVersion)
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
	if errors.Is(err, errRedirect) {
		return errors.New("the server answered with a redirect, which the CLI does not follow: check --addr or " + EnvAddr)
	}
	return fmt.Errorf("cannot reach the server: check --addr or %s and that the server is running", EnvAddr)
}

// parseOK hands raw to the generated parse only when the server answered 200.
// Any other answer is read by apiError alone: the generated parser fails on a
// 429 whose Retry-After is not an integer, which would turn the 429 into a
// transport failure.
func parseOK[T any](raw *http.Response, parse func(*http.Response) (T, error)) (T, error) {
	var zero T
	if raw.StatusCode != http.StatusOK {
		body, err := io.ReadAll(raw.Body)
		_ = raw.Body.Close()
		if err != nil {
			return zero, transportError(err)
		}
		return zero, apiError(raw, body)
	}
	resp, err := parse(raw)
	if err != nil {
		return zero, transportError(err)
	}
	return resp, nil
}

// apiError words an error response. contract.DecodeAPIError passes on only
// the status, the cleaned message and hint of the server's envelope, and the
// wait of a valid Retry-After.
//
// A wait or a 429 gets its own hint instead of the server's, which points at
// a header the user does not see. Without a valid wait it says none, rather
// than "0 seconds" or the server's raw value.
func apiError(resp *http.Response, body []byte) error {
	e := contract.DecodeAPIError(resp.StatusCode, resp.Header, body)
	status := http.StatusText(e.Status)
	msg := fmt.Sprintf("%s (%d %s)", e.Message, e.Status, status)
	if e.Message == "" {
		msg = fmt.Sprintf("the server answered %d %s", e.Status, status)
	}
	hint := e.Hint
	switch {
	case e.RetryAfter > 0:
		hint = fmt.Sprintf("Wait %s, then retry.", seconds(e.RetryAfter))
	case e.Status == http.StatusTooManyRequests:
		hint = "Wait a little, then retry."
	}
	if hint != "" {
		msg += "\nHint: " + hint
	}
	return errors.New(msg)
}

// seconds words a wait of n whole seconds.
func seconds(n int) string {
	if n == 1 {
		return "1 second"
	}
	return fmt.Sprintf("%d seconds", n)
}

func cleanList(l []string) string {
	if len(l) == 0 {
		return "(none)"
	}
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = contract.CleanLine(s)
	}
	return strings.Join(out, ", ")
}
