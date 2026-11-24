package platform

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// GitHub sign-in settings (#642, slice 1 of #25). They only tell goldie which
// OAuth app, which GitHub, which orgs and which operator teams to trust; the
// sign-in flow reads them in later slices. Every error names its variable and
// never a value: a value may be the client secret or text an attacker chose.
const (
	EnvGitHubClientID     = "GOLDIE_GITHUB_CLIENT_ID"
	EnvGitHubClientSecret = "GOLDIE_GITHUB_CLIENT_SECRET" // secret: never logged
	EnvGitHubURL          = "GOLDIE_GITHUB_URL"
	EnvGitHubOrgs         = "GOLDIE_GITHUB_ORGS"
	EnvGitHubTeams        = "GOLDIE_GITHUB_OPERATOR_TEAMS"
	EnvPublicURL          = "GOLDIE_PUBLIC_URL"
)

// githubConfig is the GitHub sign-in part of Config.
type githubConfig struct {
	GitHubClientID     string
	GitHubClientSecret string   // secret
	GitHubURL          string   // empty: github.com
	GitHubOrgs         []string // lower-cased logins
	GitHubTeams        []string // lower-cased org/slug, each org in GitHubOrgs
	PublicURL          string   // the OAuth redirect URI is built from this, never from Host
}

// loadGitHub reads and checks the sign-in settings, appending every problem to
// errs. None set is valid: sign-in stays off.
func loadGitHub(str func(name, def string) string, errs *[]error) githubConfig {
	g := githubConfig{
		GitHubClientID:     str(EnvGitHubClientID, ""),
		GitHubClientSecret: str(EnvGitHubClientSecret, ""),
		GitHubURL:          str(EnvGitHubURL, ""),
		PublicURL:          str(EnvPublicURL, ""),
	}
	bad := func(name, why string) { *errs = append(*errs, fmt.Errorf("%s: %s", name, why)) }
	for _, u := range []struct{ name, value string }{{EnvGitHubURL, g.GitHubURL}, {EnvPublicURL, g.PublicURL}} {
		if u.value != "" && !validBaseURL(u.value) {
			bad(u.name, "must be an https URL with a host and no credentials, query or fragment (http only for localhost or a loopback address)")
		}
	}
	orgs, orgsSet := splitList(str(EnvGitHubOrgs, ""))
	teams, teamsSet := splitList(str(EnvGitHubTeams, ""))
	listed := map[string]bool{}
	for _, o := range orgs {
		if !validLogin(o) {
			bad(EnvGitHubOrgs, "must be comma-separated GitHub organisation logins")
			break
		}
		listed[strings.ToLower(o)] = true
		g.GitHubOrgs = append(g.GitHubOrgs, strings.ToLower(o))
	}
	for _, t := range teams {
		org, slug, ok := strings.Cut(t, "/")
		if !ok || !validLogin(org) || !validSlug(slug) {
			bad(EnvGitHubTeams, "must be comma-separated org/slug pairs")
			break
		}
		if !listed[strings.ToLower(org)] {
			bad(EnvGitHubTeams, "every team's org must be listed in "+EnvGitHubOrgs)
			break
		}
		g.GitHubTeams = append(g.GitHubTeams, strings.ToLower(t))
	}
	if orgsSet || teamsSet || g.GitHubClientID != "" || g.GitHubClientSecret != "" || g.GitHubURL != "" {
		for _, r := range []struct{ name, value string }{
			{EnvGitHubClientID, g.GitHubClientID},
			{EnvGitHubClientSecret, g.GitHubClientSecret},
			{EnvPublicURL, g.PublicURL},
		} {
			if r.value == "" {
				bad(r.name, "is required when GitHub sign-in is configured")
			}
		}
		if !orgsSet {
			bad(EnvGitHubOrgs, "is required when GitHub sign-in is configured")
		}
	}
	return g
}

// splitList splits a comma-separated list into trimmed items; set reports
// whether the variable held anything. An empty item stays, so it is refused.
func splitList(s string) (items []string, set bool) {
	if strings.TrimSpace(s) == "" {
		return nil, false
	}
	for p := range strings.SplitSeq(s, ",") {
		items = append(items, strings.TrimSpace(p))
	}
	return items, true
}

const (
	alnum     = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	loginRune = alnum + "-"
	slugRune  = alnum + "-_."
)

// validLogin reports whether s is a GitHub login: 1 to 39 ASCII letters,
// digits and single hyphens, neither first nor last a hyphen.
func validLogin(s string) bool {
	if len(s) == 0 || len(s) > 39 || s[0] == '-' || s[len(s)-1] == '-' || strings.Contains(s, "--") {
		return false
	}
	return strings.Trim(s, loginRune) == ""
}

// validSlug reports whether s is a team slug: 1 to 100 ASCII letters, digits,
// hyphens, underscores and dots.
func validSlug(s string) bool {
	return len(s) > 0 && len(s) <= 100 && strings.Trim(s, slugRune) == ""
}

// validBaseURL reports whether s is an https URL, or an http one for
// localhost or a loopback address, with a host and without userinfo, query or
// fragment. Control characters make url.Parse fail.
func validBaseURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return false
	}
	host := u.Hostname()
	if host == "" || strings.ContainsAny(s, " \t\r\n\\#") {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		ip := net.ParseIP(host)
		return strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
	}
	return false
}
