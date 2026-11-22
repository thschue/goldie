package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf16"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/contract/project"
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
	proj, err := c.project()
	if err != nil {
		return err
	}
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	raw, err := cl.SearchArticles(ctx, &contract.SearchArticlesParams{Q: q, Project: projectParam(proj)})
	if err != nil {
		return transportError(err)
	}
	resp, e, err := contract.ParseOK(raw, contract.ParseSearchArticlesResponse)
	if err := answerError(e, err); err != nil {
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
	if dotOnly(id) {
		return errors.New("lookup needs an article id, not an empty or dot-only one\nHint: find ids with: goldie search <words>")
	}
	proj, err := c.project()
	if err != nil {
		return err
	}
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	raw, err := cl.GetArticle(ctx, id, &contract.GetArticleParams{Project: projectParam(proj)})
	if err != nil {
		return transportError(err)
	}
	resp, e, err := contract.ParseOK(raw, contract.ParseGetArticleResponse)
	if err := answerError(e, err); err != nil {
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
// The API document takes the same token as the API (#101), so the token goes
// to both. Without a token, or with a rejected one, the document's 401 still
// shows the server is reachable, and the API name stays unknown.
//
// TODO(#6): the token probe is a real search, so it will count as search and
// Miss activity once those are recorded. Replace it with the status route.
func (c *commander) status(ctx context.Context) error {
	given := c.s.token != ""
	cl, err := c.client(given)
	if err != nil {
		return err
	}
	doc, err := cl.GetOpenAPIJSONWithResponse(ctx)
	if err != nil {
		return transportError(err)
	}
	if doc.JSON200 == nil && doc.StatusCode() != http.StatusUnauthorized {
		return apiError(doc.HTTPResponse, doc.Body)
	}
	// A Project problem is reported on the Project line, not as a failure:
	// status is how a user finds out what is wrong. Other commands still fail.
	proj, perr := c.project()
	projectError := ""
	if perr != nil {
		projectError = contract.CleanLine(perr.Error())
		proj = project.Detected{}
	}
	auth := "no token given (set --token or " + EnvToken + " to check it)"
	accepted := false
	var title, apiVersion string
	var hint *string
	switch {
	case doc.JSON200 != nil:
		title, apiVersion = specInfo(*doc.JSON200)
		if given {
			if auth, accepted, hint, err = probeToken(ctx, cl, projectParam(proj)); err != nil {
				return err
			}
		}
	case given:
		auth = "token rejected"
	}
	if c.s.json {
		out := map[string]any{"reachable": true, "api": nil, "apiVersion": nil,
			"tokenGiven": given, "tokenAccepted": accepted, "project": nil, "projectSource": nil}
		if doc.JSON200 != nil {
			out["api"], out["apiVersion"] = title, apiVersion
		}
		if proj.ID != "" {
			out["project"], out["projectSource"] = proj.ID, sourceWords[proj.Source]
		}
		if projectError != "" {
			out["projectError"] = projectError
		}
		if h := projectHint(proj); h != "" {
			out["projectHint"] = h
		}
		if hint != nil && *hint != "" {
			out["hint"] = contract.CleanLine(*hint)
		}
		return c.printJSON(out)
	}
	apiLine := "unknown (the API document needs an accepted token)"
	if doc.JSON200 != nil {
		apiLine = contract.CleanLine(title) + " " + contract.CleanLine(apiVersion)
	}
	_, err = fmt.Fprintf(c.out, "Server: reachable\nAPI: %s\nAuth: %s\nProject: %s\n\nSearch with: goldie search <words>\n",
		apiLine, auth, describeProject(proj, projectError))
	if err == nil {
		var b strings.Builder
		writeHint(&b, hint)
		_, err = fmt.Fprint(c.out, b.String())
	}
	return err
}

// probeToken checks the token with one search for project, so status reports
// the token as accepted only where the API answers it, and passes on the
// server's hint for an unregistered Project. Any answer other than 200 or 401
// is read by contract.ParseOK, so a 429 stays a rate-limit error.
func probeToken(ctx context.Context, cl *contract.ClientWithResponses, project *contract.ProjectId) (auth string, accepted bool, hint *string, err error) {
	raw, err := cl.SearchArticles(ctx, &contract.SearchArticlesParams{Q: "goldie", Project: project})
	if err != nil {
		return "", false, nil, transportError(err)
	}
	if raw.StatusCode == http.StatusUnauthorized {
		_ = raw.Body.Close()
		return "token rejected", false, nil, nil
	}
	resp, e, err := contract.ParseOK(raw, contract.ParseSearchArticlesResponse)
	if err := answerError(e, err); err != nil {
		return "", false, nil, err
	}
	if resp.JSON200 == nil {
		return "", false, nil, apiError(resp.HTTPResponse, resp.Body)
	}
	return "token accepted", true, resp.JSON200.Hint, nil
}

// version prints the client version, and the server's API version when an
// address is configured and the server answers. The server part is best
// effort: GOLDIE_ADDR is also the server's listen address (":8080"), so an
// address that is not a URL is reported as not set instead of failing the
// command. The API document takes the API's token (#101), so it is sent when
// one is set.
//
// When the version is missing the output says why, with a hint where the user
// can act, as status does on its Auth line: no token, token rejected, a
// redirect (check the address), an answer that was too large, or an
// unreachable server (check that the server is running and the address is
// right). It never prints the address or the token. The JSON key stays
// serverApiVersion (the contract is additive-only) and is null when unknown,
// with the reason in serverNote.
func (c *commander) version(ctx context.Context) error {
	info := map[string]any{"version": platform.Version}
	line := platform.Version
	var note string
	var hint *string
	cl, err := c.client(c.s.token != "")
	var doc *contract.GetOpenAPIJSONResponse
	if err == nil {
		doc, err = cl.GetOpenAPIJSONWithResponse(ctx)
	}
	switch {
	case cl == nil:
		// client only fails on an unusable address here: the token is
		// requested only when one is set.
		note = "no server address set"
		h := "set " + EnvAddr + " to the server address to see the server API version"
		hint = &h
	case errors.Is(err, contract.ErrRedirect):
		note = "the server answered with a redirect, which the CLI does not follow"
		h := "check the address (--addr or " + EnvAddr + ")"
		hint = &h
	case errors.Is(err, contract.ErrTooLarge):
		note = "the server's answer was too large"
		h := "check that the address points at a Goldie server"
		hint = &h
	case err != nil:
		note = "the server cannot be reached"
		h := "check that the server is running and the address is right"
		hint = &h
	case doc.JSON200 != nil:
		_, apiVersion := specInfo(*doc.JSON200)
		info["serverApiVersion"] = apiVersion
		return c.printVersion(info, line+"\nserver API: "+contract.CleanLine(apiVersion))
	case doc.StatusCode() == http.StatusUnauthorized && c.s.token == "":
		note = "no token given"
		h := "set " + EnvToken + " or " + EnvToken + "_FILE (or pass --token) to see the server API version"
		hint = &h
	case doc.StatusCode() == http.StatusUnauthorized:
		note = "the token was rejected"
		h := "check " + EnvToken + " or " + EnvToken + "_FILE"
		hint = &h
	default:
		note = "the server did not return its API document"
	}
	info["serverApiVersion"] = nil
	info["serverNote"] = note
	line += "\nserver API: unknown (" + note + ")"
	if hint != nil {
		info["hint"] = *hint
		var b strings.Builder
		writeHint(&b, hint)
		line += "\n" + strings.TrimSuffix(b.String(), "\n")
	}
	return c.printVersion(info, line)
}

// printVersion writes the version report as JSON or as text.
func (c *commander) printVersion(info map[string]any, line string) error {
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
	if errors.Is(err, contract.ErrTooLarge) {
		return fmt.Errorf("the server's answer is larger than %d MiB", contract.MaxResponseBytes>>20)
	}
	if errors.Is(err, contract.ErrRedirect) {
		return errors.New("the server answered with a redirect, which the CLI does not follow: check --addr or " + EnvAddr)
	}
	return fmt.Errorf("cannot reach the server: check --addr or %s and that the server is running", EnvAddr)
}

// apiError words an error response. contract.DecodeAPIError passes on only
// the status, the cleaned message and hint of the server's envelope, and the
// wait of a valid Retry-After.
//
// A wait or a 429 gets its own hint instead of the server's, which points at
// a header the user does not see. Without a valid wait it says none, rather
// than "0 seconds" or the server's raw value.
func apiError(resp *http.Response, body []byte) error {
	return wordAPIError(contract.DecodeAPIError(resp.StatusCode, resp.Header, body))
}

// answerError words what contract.ParseOK found wrong with an answer: an
// unreadable or unparsable body as transportError, an error status as
// apiError. It is nil for a 200.
func answerError(e *contract.APIError, err error) error {
	switch {
	case err != nil:
		return transportError(err)
	case e != nil:
		return wordAPIError(*e)
	}
	return nil
}

// wordAPIError is the text of e, as apiError describes.
func wordAPIError(e contract.APIError) error {
	status := http.StatusText(e.Status)
	msg := fmt.Sprintf("%s (%d %s)", e.Message, e.Status, status)
	if e.Message == "" {
		msg = fmt.Sprintf("the server answered %d %s", e.Status, status)
	}
	hint := e.Hint
	switch {
	case e.Wait() != "":
		hint = "Wait " + e.Wait() + ", then retry."
	case e.Status == http.StatusTooManyRequests:
		hint = "Wait a little, then retry."
	}
	if hint != "" {
		msg += "\nHint: " + hint
	}
	return errors.New(msg)
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

// dotOnly reports whether id is empty or only dots. Such an id would be
// resolved by the URL path ("." and ".." collapse) into a different API route,
// so it is refused before any Project, client or HTTP step.
func dotOnly(id string) bool {
	return strings.Trim(id, ".") == ""
}
