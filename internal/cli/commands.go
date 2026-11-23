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
		case r < 0x7f || !contract.HiddenRune(r):
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
	res, err := contract.Answer(raw, err, contract.ParseSearchArticlesResponse, searchResults)
	if err != nil {
		return wordError(err, c.local)
	}
	if c.s.json {
		return c.printJSON(res)
	}
	var b strings.Builder
	hits := res.Results
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
			fmt.Fprintf(&b, "    score %d, matched: %s\n", h.Score, contract.CleanList(h.Matched))
			fmt.Fprintf(&b, "    keywords: %s\n", contract.CleanList(h.Keywords))
		}
		b.WriteString("\nRead one with: goldie lookup <id>\n")
	}
	b.WriteString("Try other words: goldie search <words>\n")
	writeHint(&b, res.Hint)
	_, err = fmt.Fprint(c.out, b.String())
	return err
}

// searchResults is the 200 body of a search, for contract.Answer.
func searchResults(r *contract.SearchArticlesResponse) *contract.SearchResults { return r.JSON200 }

func (c *commander) lookup(ctx context.Context, id string) error {
	// An empty or dot-only id would be resolved by the URL path ("." and ".."
	// collapse) into a different API route, so it is refused before any
	// Project, client or HTTP step.
	if contract.CheckArticleID(id) != nil {
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
	a, err := contract.Answer(raw, err, contract.ParseGetArticleResponse,
		func(r *contract.GetArticleResponse) *contract.Article { return r.JSON200 })
	if err != nil {
		// Only lookup words the plain-miss hint: other commands keep the
		// server's hint, and so does any other not_found, such as a
		// Suppression's, whose hint names the entry to remove.
		return wordError(contract.RewordMiss(err, contract.ArticleMissHint,
			"Search for the article with: goldie search <words>, then use an id from its results."), c.local)
	}
	// Only the warning goes to stderr, so stdout stays the article alone and
	// --json stays one machine-readable document, lifecycle fields included.
	if ref, deprecated := contract.Deprecation(*a); deprecated {
		_, _ = fmt.Fprintln(c.errOut, deprecationNotice(ref))
	}
	if c.s.json {
		return c.printJSON(a)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nid: %s\nkeywords: %s\n\n%s\n\nFind more with: goldie search <words>\n",
		contract.CleanLine(a.Name), contract.CleanLine(a.Id), contract.CleanList(a.Keywords), strings.TrimRight(contract.CleanText(a.Content), "\n"))
	writeHint(&b, a.Hint)
	_, err = fmt.Fprint(c.out, b.String())
	return err
}

// deprecationNotice is the stderr line for a deprecated article; ref is the
// cleaned replacement contract.Deprecation gives, or "".
func deprecationNotice(ref string) string {
	if ref == "" {
		return "Warning: this article is deprecated."
	}
	return "Warning: this article is deprecated. Use " + ref + " instead."
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
	probe := probeServer(ctx, cl, given)
	if probe.err != nil {
		return wordError(probe.err, c.local)
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
	var hint *string
	switch {
	case probe.answered():
		if given {
			if auth, accepted, hint, err = probeToken(ctx, cl, projectParam(proj), c.local); err != nil {
				return err
			}
		}
	case probe.tokenState == tokenRejected:
		auth = "token rejected"
	}
	if c.tokenBroken != "" {
		auth = c.tokenBroken
	}
	var suppressions []string
	if accepted && proj.ID != "" {
		suppressions = projectSuppressions(ctx, cl, proj.ID)
	}
	if c.s.json {
		out := map[string]any{"reachable": true, "api": nil, "apiVersion": nil,
			"tokenGiven": given, "tokenAccepted": accepted, "project": nil, "projectSource": nil}
		if probe.answered() {
			out["api"], out["apiVersion"] = probe.title, probe.apiVersion
		}
		if proj.ID != "" {
			out["project"], out["projectSource"] = proj.ID, sourceWords[proj.Source]
		}
		if len(suppressions) > 0 {
			out["suppressions"] = suppressions
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
	if probe.answered() {
		apiLine = contract.CleanLine(probe.title) + " " + contract.CleanLine(probe.apiVersion)
	}
	suppressionLine := ""
	if len(suppressions) > 0 {
		suppressionLine = "Suppressions: " + strings.Join(suppressions, ", ") + "\n"
	}
	_, err = fmt.Fprintf(c.out, "Server: reachable\nAPI: %s\nAuth: %s\nProject: %s\n%s\nSearch with: goldie search <words>\n",
		apiLine, auth, describeProject(proj, projectError), suppressionLine)
	if err == nil {
		var b strings.Builder
		writeHint(&b, hint)
		_, err = fmt.Fprint(c.out, b.String())
	}
	return err
}

// projectSuppressions lists the Suppressions the server holds for id, each
// cleaned to one printable line. It tolerates every failure, a 404 for a
// Project the server does not hold included: status still reports the rest, so
// a server without the Project detail route shows no Suppressions.
func projectSuppressions(ctx context.Context, cl *contract.ClientWithResponses, id string) []string {
	raw, err := cl.GetProject(ctx, id)
	detail, err := contract.Answer(raw, err, contract.ParseGetProjectResponse,
		func(r *contract.GetProjectResponse) *contract.ProjectDetail { return r.JSON200 })
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(detail.Suppressions))
	for _, ref := range detail.Suppressions {
		out = append(out, contract.CleanLine(ref))
	}
	return out
}

// probeToken checks the token with one search for project, so status reports
// the token as accepted only where the API answers it, and passes on the
// server's hint for an unregistered Project. Any answer other than 401 is read
// by contract.Answer, so a 429 stays a rate-limit error.
func probeToken(ctx context.Context, cl *contract.ClientWithResponses, project *contract.ProjectId, local bool) (auth string, accepted bool, hint *string, err error) {
	raw, err := cl.SearchArticles(ctx, &contract.SearchArticlesParams{Q: "goldie", Project: project})
	if err == nil && raw.StatusCode == http.StatusUnauthorized {
		_ = raw.Body.Close()
		return "token rejected", false, nil, nil
	}
	res, err := contract.Answer(raw, err, contract.ParseSearchArticlesResponse, searchResults)
	if err != nil {
		return "", false, nil, wordError(err, local)
	}
	return "token accepted", true, res.Hint, nil
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
// redirect (check the address), an answer that was too large, an error
// status, an answer that isn't a goldie API answer (check the address), or an
// unreachable server (check that the server is running and the address is
// right). For an error status the note is the cleaned message of the server's
// envelope with the status (just the status when there is no message), and the
// hint is the server's own, or a wait hint when the server sent a valid
// Retry-After or answered 429 (wordAPIError). It never prints the address or
// the token. The JSON key stays
// serverApiVersion (the contract is additive-only) and is null when unknown,
// with the reason in serverNote.
func (c *commander) version(ctx context.Context) error {
	info := map[string]any{"version": platform.Version}
	line := platform.Version
	var note string
	var hint *string
	given := c.s.token != ""
	cl, err := c.client(given)
	var probe serverProbe
	var apiErr *contract.APIError
	if err == nil {
		probe = probeServer(ctx, cl, given)
		err = probe.err
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
	case errors.Is(err, contract.ErrNotAPIAnswer):
		note = "the server's answer isn't a goldie API answer"
		h := "check the address (--addr or " + EnvAddr + ")"
		hint = &h
	case errors.As(err, &apiErr):
		var h string
		note, h = wordAPIError(apiErr)
		if h != "" {
			hint = &h
		}
	case err != nil:
		note = "the server cannot be reached"
		h := "check that the server is running and the address is right"
		hint = &h
	case probe.answered():
		info["serverApiVersion"] = probe.apiVersion
		return c.printVersion(info, line+"\nserver API: "+contract.CleanLine(probe.apiVersion))
	case probe.tokenState == tokenMissing && c.tokenBroken != "":
		note = c.tokenBroken
		h := "check " + EnvToken + " and " + EnvToken + "_FILE"
		hint = &h
	case probe.tokenState == tokenMissing:
		note = "no token given"
		h := "set " + EnvToken + " or " + EnvToken + "_FILE (or pass --token) to see the server API version"
		hint = &h
	case probe.tokenState == tokenRejected:
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

// wordError words a failure to call the API or read its answer, as
// contract.Answer classifies it. A transport failure loses its cause, which
// can carry the address (and the user already knows what they typed). In
// local mode there is no address or token to check: the embedded server is
// the cause.
//
// For an error status, contract.DecodeAPIError passes on only the status, the
// cleaned message and hint of the server's envelope, and the wait of a valid
// Retry-After. A wait or a 429 gets its own hint instead of the server's,
// which points at a header the user does not see. Without a valid wait it
// says none, rather than "0 seconds" or the server's raw value.
func wordError(err error, local bool) error {
	var e *contract.APIError
	switch {
	case errors.As(err, &e):
	case errors.Is(err, contract.ErrTooLarge):
		return fmt.Errorf("the server's answer is larger than %d MiB", contract.MaxResponseBytes>>20)
	case errors.Is(err, contract.ErrRedirect) && local:
		return errors.New("the embedded server answered with a redirect, which the CLI does not follow")
	case errors.Is(err, contract.ErrRedirect):
		return errors.New("the server answered with a redirect, which the CLI does not follow: check --addr or " + EnvAddr)
	case errors.Is(err, contract.ErrNotAPIAnswer) && local:
		return errors.New("the embedded server's answer isn't a goldie API answer")
	case errors.Is(err, contract.ErrNotAPIAnswer):
		return errors.New("the server's answer isn't a goldie API answer; check the address")
	case local:
		return errors.New("cannot reach the embedded server over the local content: it stopped or could not be reached")
	default:
		return fmt.Errorf("cannot reach the server: check --addr or %s and that the server is running", EnvAddr)
	}
	msg, hint := wordAPIError(e)
	if hint != "" {
		msg += "\nHint: " + hint
	}
	return errors.New(msg)
}

// wordAPIError words an error status as a note and a hint, which status joins
// into one message (wordError) and version prints on its own lines. The hint
// is empty when there is none.
func wordAPIError(e *contract.APIError) (note, hint string) {
	status := http.StatusText(e.Status)
	note = fmt.Sprintf("%s (%d %s)", e.Message, e.Status, status)
	if e.Message == "" {
		note = fmt.Sprintf("the server answered %d %s", e.Status, status)
	}
	hint = e.Hint
	// The API words some hints for HTTP clients; the CLI replaces them by
	// error code with its own ways to fix the call.
	switch {
	case e.Code == "unauthorized":
		hint = "Set --token, " + EnvToken + " or " + EnvToken + "_FILE to a valid API token."
	case e.Wait() != "":
		hint = "Wait " + e.Wait() + ", then retry."
	case e.Status == http.StatusTooManyRequests:
		hint = "Wait a little, then retry."
	}
	return note, hint
}
