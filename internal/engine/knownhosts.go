package engine

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // known_hosts hashes host names with HMAC-SHA1; this only matches them
	"encoding/base64"
	"errors"
	"io"
	"net"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// errHostKeyNotTrusted is what the host key check returns, whatever the
// reason, so fetchError can map every host key failure to one message.
var errHostKeyNotTrusted = errors.New("ssh host key not trusted")

// knownHosts is parsed known_hosts content, checked from memory: x/crypto's
// knownhosts package only reads files, and the engine reads none (PC-11).
//
// Supported: plain and hashed (|1|salt|hash) host names, [host]:port, negated
// patterns and @revoked. Wildcard patterns never match and @cert-authority
// lines are ignored, so both fail closed.
type knownHosts []knownHost

type knownHost struct {
	revoked  bool
	patterns []string
	key      []byte // wire format
	keyType  string
}

// parseKnownHosts parses content and requires at least one trusted key. Its
// errors are fixed: they never echo the content.
func parseKnownHosts(content string) (knownHosts, error) {
	var out knownHosts
	trusted := 0
	rest := []byte(content)
	for {
		marker, hosts, key, _, next, err := ssh.ParseKnownHosts(rest)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("git source: KnownHosts is not valid known_hosts content")
		}
		rest = next
		switch marker {
		case "":
			trusted++
		case "revoked":
		default:
			continue
		}
		out = append(out, knownHost{revoked: marker == "revoked", patterns: hosts, key: key.Marshal(), keyType: key.Type()})
	}
	if trusted == 0 {
		return nil, errors.New("git source: KnownHosts lists no host key")
	}
	return out, nil
}

// check is an ssh.HostKeyCallback: the key must be listed for the host and not
// revoked for any host.
func (kh knownHosts) check(hostname string, _ net.Addr, key ssh.PublicKey) error {
	host := knownhosts.Normalize(hostname)
	wire := key.Marshal()
	for _, e := range kh {
		if e.revoked && bytes.Equal(e.key, wire) {
			return errHostKeyNotTrusted
		}
	}
	for _, e := range kh {
		if !e.revoked && bytes.Equal(e.key, wire) && e.matches(host) {
			return nil
		}
	}
	return errHostKeyNotTrusted
}

// algorithms lists the host key algorithms of the keys trusted for hostport,
// so the server is asked for a key type check can verify. Nil means the
// library default; check then rejects whatever the server offers.
func (kh knownHosts) algorithms(hostport string) []string {
	host := knownhosts.Normalize(hostport)
	var out []string
	for _, e := range kh {
		if e.revoked || !e.matches(host) {
			continue
		}
		algos := []string{e.keyType}
		if e.keyType == ssh.KeyAlgoRSA {
			algos = []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
		}
		for _, a := range algos {
			if !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	return out
}

// matches reports whether a normalized host matches the entry's patterns. A
// matching negated pattern excludes the host.
func (e knownHost) matches(host string) bool {
	matched := false
	for _, p := range e.patterns {
		p, negated := strings.CutPrefix(p, "!")
		if !strings.EqualFold(p, host) && !hashedMatch(p, host) {
			continue
		}
		if negated {
			return false
		}
		matched = true
	}
	return matched
}

// hashedMatch checks a |1|salt|hash pattern: hash = HMAC-SHA1(salt, host).
func hashedMatch(pattern, host string) bool {
	parts := strings.Split(pattern, "|")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "1" {
		return false
	}
	salt, err1 := base64.StdEncoding.DecodeString(parts[2])
	sum, err2 := base64.StdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(host))
	return hmac.Equal(mac.Sum(nil), sum)
}
