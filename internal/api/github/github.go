// Package github asks GitHub who someone is and which Role their organisation
// and team membership earns (slice 2 of #25).
//
// It is an outbound client in the style of internal/contract/client.go: calls
// time out, answers are size-capped, and errors are fixed text that never
// contains a GitHub response body, URL, code, verifier or token. The GitHub
// access token lives in one Exchange call and is neither returned nor stored.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/thschue/goldie/internal/platform"
)

const (
	defaultBaseURL = "https://github.com"
	defaultAPIURL  = "https://api.github.com"
	// defaultTimeout bounds each call to GitHub when Options.Timeout is zero.
	defaultTimeout = 10 * time.Second
	// maxAnswerBytes caps every GitHub answer; the real ones are well under 10 KiB.
	maxAnswerBytes = 1 << 20
	// maxInputBytes caps the code, verifier and redirect URI a caller hands in.
	maxInputBytes = 2048
)

// ErrNotMember means GitHub knows the user but no active membership earns a Role.
var ErrNotMember = errors.New("github: not an active member of a listed organisation or team")

var errNotFound = errors.New("github: not found")

// segment matches one GitHub org, team slug or user login (Enterprise Managed
// Users logins carry an underscore). Nothing here can steer a request path.
var segment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

// The Roles a GitHub membership can earn, by the names authorization parses.
const (
	roleReader   = "reader"
	roleOperator = "operator"
)

// Identity is a verified GitHub login and the Role it earned ("reader" or
// "operator"). It holds no token.
type Identity struct {
	Login string
	Role  string
}

// Options configures a Client.
type Options struct {
	// BaseURL is the GitHub web URL; empty means https://github.com. For GitHub
	// Enterprise it is the server root, and the API is served under /api/v3.
	BaseURL      string
	ClientID     string
	ClientSecret string
	// Orgs: an active member of any of them is a reader.
	Orgs []string
	// OperatorTeams are "org/slug": an active member of any is an operator.
	OperatorTeams []string
	// Timeout bounds each call to GitHub; zero means 10 seconds.
	Timeout time.Duration
}

type team struct{ org, slug string }

// Client exchanges OAuth codes for Identities. It is safe for concurrent use.
type Client struct {
	oauthURL     string
	apiURL       string
	clientID     string
	clientSecret string
	orgs         []string
	teams        []team
	http         *http.Client
}

// New validates opts and returns a Client. Errors never echo the base URL.
func New(opts Options) (*Client, error) {
	base := opts.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("github: base URL must be an http(s) URL without credentials, query or fragment")
	}
	if u.Scheme != "https" && !platform.IsLoopbackHost(u.Hostname()) {
		return nil, errors.New("github: base URL must use https unless its host is loopback")
	}
	root := strings.TrimRight(u.String(), "/")
	api := root + "/api/v3"
	if strings.EqualFold(u.Hostname(), "github.com") {
		api = defaultAPIURL
	}
	if opts.ClientID == "" || opts.ClientSecret == "" {
		return nil, errors.New("github: client ID and client secret are required")
	}
	c := &Client{oauthURL: root, apiURL: api, clientID: opts.ClientID, clientSecret: opts.ClientSecret}
	for _, o := range opts.Orgs {
		if !segment.MatchString(o) {
			return nil, fmt.Errorf("github: invalid organisation %q", o)
		}
		c.orgs = append(c.orgs, o)
	}
	for _, t := range opts.OperatorTeams {
		org, slug, ok := strings.Cut(t, "/")
		if !ok || !segment.MatchString(org) || !segment.MatchString(slug) {
			return nil, fmt.Errorf("github: invalid operator team %q, want org/slug", t)
		}
		c.teams = append(c.teams, team{org, slug})
	}
	if len(c.orgs) == 0 && len(c.teams) == 0 {
		return nil, errors.New("github: at least one organisation or operator team is required")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	// A redirect is never followed: it would carry the token elsewhere.
	c.http = &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c, nil
}

// String and GoString keep the client secret out of any formatted output.
func (c *Client) String() string   { return "github.Client" }
func (c *Client) GoString() string { return "github.Client" }

// Exchange trades an OAuth code (with its PKCE verifier and redirect URI) for
// the user's login, then maps their memberships to a Role. It returns
// ErrNotMember when no active membership earns one. Any other failure is a
// rejection too: no Role is ever the result of an error.
func (c *Client) Exchange(ctx context.Context, code, verifier, redirectURI string) (Identity, error) {
	for _, in := range []string{code, verifier, redirectURI} {
		if in == "" || len(in) > maxInputBytes {
			return Identity{}, errors.New("github: code, verifier and redirect URI are required")
		}
	}
	token, err := c.token(ctx, code, verifier, redirectURI)
	if err != nil {
		return Identity{}, err
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := c.getJSON(ctx, token, "/user", &user); err != nil {
		return Identity{}, err
	}
	if !segment.MatchString(user.Login) {
		return Identity{}, errors.New("github: GitHub returned an unusable login")
	}
	role, err := c.role(ctx, token, user.Login)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Login: user.Login, Role: role}, nil
}

func (c *Client) role(ctx context.Context, token, login string) (string, error) {
	for _, t := range c.teams {
		p := "/orgs/" + url.PathEscape(t.org) + "/teams/" + url.PathEscape(t.slug) + "/memberships/" + url.PathEscape(login)
		active, err := c.active(ctx, token, p)
		if err != nil {
			return "", err
		}
		if active {
			return roleOperator, nil
		}
	}
	for _, o := range c.orgs {
		active, err := c.active(ctx, token, "/user/memberships/orgs/"+url.PathEscape(o))
		if err != nil {
			return "", err
		}
		if active {
			return roleReader, nil
		}
	}
	return "", ErrNotMember
}

// active reports whether a membership endpoint answers an active membership.
// A 404 is "not a member"; any other non-2xx is an error.
func (c *Client) active(ctx context.Context, token, path string) (bool, error) {
	var m struct {
		State string `json:"state"`
	}
	err := c.getJSON(ctx, token, path, &m)
	if errors.Is(err, errNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return m.State == "active", nil
}

func (c *Client) token(ctx context.Context, code, verifier, redirectURI string) (string, error) {
	form := url.Values{
		"client_id": {c.clientID}, "client_secret": {c.clientSecret},
		"code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("github: cannot build the token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := c.do(req, &out); err != nil {
		if errors.Is(err, errNotFound) {
			return "", errors.New("github: unexpected status 404")
		}
		return "", err
	}
	if out.Error != "" || out.AccessToken == "" {
		return "", errors.New("github: the code exchange was refused")
	}
	return out.AccessToken, nil
}

func (c *Client) getJSON(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL+path, nil)
	if err != nil {
		return errors.New("github: cannot build the request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	return c.do(req, out)
}

// do runs req and decodes a 2xx JSON answer into out. Every error is fixed
// text: no URL, body or credential, and not the transport's own error.
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return fmt.Errorf("github: request abandoned: %w", ctxErr)
		}
		return errors.New("github: request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("github: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes+1))
	if err != nil {
		return errors.New("github: reading the answer failed")
	}
	if len(body) > maxAnswerBytes {
		return errors.New("github: the answer is too large")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("github: the answer is not valid JSON")
	}
	return nil
}
