package engine

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
)

// GitOptions configures a git Source. Credentials are passed in as values: the
// engine reads no environment, file or SSH agent for them.
type GitOptions struct {
	// URL is https://host/path, ssh://[user@]host[:port]/path, the scp-like
	// user@host:path or file:///path. It carries no password; credentials go
	// in Token or SSHKey. An ssh URL's user is the login name, git when there
	// is none. It never starts with "-".
	URL string
	// Ref is a branch, a tag or a full ref name (refs/...). Empty means the
	// remote's HEAD. It never starts with "-".
	Ref string
	// Path is a directory of the repository to serve instead of its root,
	// slash-separated and relative: no leading or trailing slash, no "." or
	// ".." element. It is checked at each Load, which fails with ErrGitPath
	// when it is invalid or not a directory at the served ref. Empty serves
	// the root.
	Path string
	// Token is an HTTPS access token, sent as the basic-auth password. It is
	// only accepted with an https URL.
	Token string
	// SSHKey is an unencrypted PEM private key, only accepted with an ssh URL,
	// which needs one.
	SSHKey string
	// KnownHosts is known_hosts content, which an ssh URL needs: the server's
	// host key is always checked against it, and nothing skips that check.
	KnownHosts string
	// CABundle is PEM certificates to trust, in addition to the system's, for
	// an https server behind an internal CA. It is only accepted with an https
	// URL.
	CABundle string
}

// String describes o with the URL, the token, the SSH key, the known hosts
// and the CA bundle shown only as set or unset, so logging GitOptions with %v
// or %+v never prints a credential. No part of the URL is printed: no parse
// tells every password from a path. The composition root logs the URL itself,
// through platform's redactor.
func (o GitOptions) String() string {
	return fmt.Sprintf("{URL:%s Ref:%q Token:%s SSHKey:%s KnownHosts:%s CABundle:%s}", redactToken(o.URL), o.Ref,
		redactToken(o.Token), redactToken(o.SSHKey), redactToken(o.KnownHosts), redactToken(o.CABundle))
}

// GoString is String, for %#v.
func (o GitOptions) GoString() string { return "engine.GitOptions" + o.String() }

// LogValue implements slog.LogValuer with the same redaction as String.
func (o GitOptions) LogValue() slog.Value {
	return slog.GroupValue(slog.String("url", redactToken(o.URL)), slog.String("ref", o.Ref),
		slog.String("token", redactToken(o.Token)), slog.String("ssh_key", redactToken(o.SSHKey)),
		slog.String("known_hosts", redactToken(o.KnownHosts)), slog.String("ca_bundle", redactToken(o.CABundle)))
}

// redactToken shows only whether t is set.
func redactToken(t string) string {
	if t == "" {
		return ""
	}
	return "[redacted]"
}

// gitLoadTimeout bounds one Load inside its context, so a stalled server cannot
// hold the Syncer even when the context has no deadline.
const gitLoadTimeout = 2 * time.Minute

// sshUser is the login name for an ssh URL without a user.
const sshUser = "git"

// tokenUser is the basic-auth user name sent with Token. GitHub and GitLab
// accept any non-empty name with an access token.
const tokenUser = "x-access-token"

var installGitTransports sync.Once

// useGitTransports installs, once per process, the go-git transports a
// GitSource needs. PC-11: no git binary, so file:// is served by go-git's
// in-process server rather than an exec'd git-upload-pack. https goes through
// a client that charges each Load's reads to its budget. ssh is go-git's
// client, with ~/.ssh/config switched off so no file can redirect a host, and
// its connections dialled by sshDialer, which applies the same bounds.
func useGitTransports() {
	installGitTransports.Do(func() {
		client.InstallProtocol("file", server.NewClient(server.DefaultLoader))
		client.InstallProtocol("https", newGitHTTPClient())
		gitssh.DefaultSSHConfig = nil
		registerSSHDialer()
	})
}

// ErrGitPath is the error of a Load whose GitOptions.Path is not valid or not
// a directory at the served ref. It names no path and no URL.
var ErrGitPath = errors.New("git source: Path is not a directory of the repository")

// GitSource is a Source backed by a git repository. Each Load lists the remote
// refs; when the ref moved, Load clones that commit into memory (no working
// tree, no disk) and reads its tree through the loader DirSource uses.
type GitSource struct {
	url     string
	ref     string
	path    string
	auth    transport.AuthMethod
	shallow bool
	// caBundle is GitOptions.CABundle, checked to hold only certificates.
	caBundle []byte
	// timeout bounds one Load; tests shorten it.
	timeout time.Duration
	// maxWire and maxMem are one Load's budget; tests lower them.
	maxWire, maxMem int64

	load    sync.Mutex    // serialises Load
	mu      sync.Mutex    // guards the fields below
	refHash plumbing.Hash // remote ref hash the cached Snapshot was read at
	rev     string        // commit SHA of the cached Snapshot
	snap    *Snapshot
}

// NewGitSource validates opts and returns a git Source. It contacts no server.
// Every error is a *GitOptionError naming the option to fix, with a fixed
// message that never contains the URL, the ref or a credential.
func NewGitSource(opts GitOptions) (*GitSource, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return nil, optionError(GitOptionURL, "URL is empty")
	}
	// A git command line reads a leading "-" as an option.
	if strings.HasPrefix(opts.URL, "-") {
		return nil, optionError(GitOptionURL, "URL must not start with -")
	}
	// A query or a fragment is where a token gets pasted, and git needs
	// neither; in a valid URL, ? and # appear only to start them.
	if strings.ContainsAny(opts.URL, "?#") {
		return nil, optionError(GitOptionURL, "URL must not carry a query or a fragment")
	}
	u, err := parseGitURL(opts.URL)
	if err != nil {
		return nil, err
	}
	scheme := strings.ToLower(u.Scheme)
	if _, hasPassword := u.User.Password(); hasPassword || (u.User != nil && scheme != "ssh") {
		return nil, optionError(GitOptionURL, "URL must not carry credentials; use Token or SSHKey")
	}
	if strings.HasPrefix(u.Hostname(), "-") {
		return nil, optionError(GitOptionURL, "URL is not valid")
	}
	if scheme != "https" && opts.Token != "" {
		return nil, optionError(GitOptionToken, "Token needs an HTTPS URL")
	}
	if scheme != "ssh" && opts.SSHKey != "" {
		return nil, optionError(GitOptionSSHKey, "SSHKey needs an ssh URL")
	}
	if scheme != "ssh" && opts.KnownHosts != "" {
		return nil, optionError(GitOptionKnownHosts, "KnownHosts needs an ssh URL")
	}
	if scheme != "https" && opts.CABundle != "" {
		return nil, optionError(GitOptionCABundle, "CABundle needs an HTTPS URL")
	}
	if opts.CABundle != "" && !isCertBundle(opts.CABundle) {
		return nil, optionError(GitOptionCABundle, "CABundle is not a PEM certificate bundle")
	}
	s := &GitSource{url: opts.URL, ref: opts.Ref, path: opts.Path, timeout: gitLoadTimeout, maxWire: maxGitWireBytes, maxMem: maxGitMemoryBytes}
	if opts.CABundle != "" {
		s.caBundle = []byte(opts.CABundle)
	}
	switch {
	case scheme == "https" && u.Host != "" && u.Opaque == "":
		s.shallow = true
		if opts.Token != "" {
			s.auth = &githttp.BasicAuth{Username: tokenUser, Password: opts.Token}
		}
	case scheme == "ssh" && u.Hostname() != "" && u.Opaque == "":
		s.shallow = true
		if s.auth, err = newSSHAuth(u, opts); err != nil {
			return nil, err
		}
	case scheme == "file" && u.Host == "" && u.Opaque == "" && u.Path != "":
		// go-git's in-process server does not serve shallow clones.
	default:
		return nil, optionError(GitOptionURL, errURLForm)
	}
	if _, err := transport.NewEndpoint(opts.URL); err != nil {
		return nil, optionError(GitOptionURL, "URL is not valid")
	}
	if strings.HasPrefix(opts.Ref, "-") {
		return nil, optionError(GitOptionRef, "Ref must not start with -")
	}
	if opts.Ref != "" && plumbing.ReferenceName(fullRef(opts.Ref)).Validate() != nil {
		return nil, optionError(GitOptionRef, "Ref is not a valid ref name")
	}
	useGitTransports()
	return s, nil
}

// isCertBundle reports whether bundle is one or more PEM certificates that
// parse, and nothing else that PEM could carry: no key, no request, no block
// cut off. Text between and around the blocks, as in a CA bundle with
// comments, is allowed.
func isCertBundle(bundle string) bool {
	rest := []byte(bundle)
	n := 0
	for {
		block, r := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = r
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return false
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return false
		}
		n++
	}
	return n > 0 && !bytes.Contains(rest, []byte("-----BEGIN"))
}

// newSSHAuth checks the SSH key and known hosts of an ssh URL u. The host key
// is always verified against KnownHosts; there is no option to skip it.
func newSSHAuth(u *url.URL, opts GitOptions) (*sshAuth, error) {
	if opts.SSHKey == "" {
		return nil, optionError(GitOptionSSHKey, "an ssh URL needs SSHKey")
	}
	if strings.TrimSpace(opts.KnownHosts) == "" {
		return nil, optionError(GitOptionKnownHosts, "an ssh URL needs KnownHosts; host keys are always verified")
	}
	signer, err := ssh.ParsePrivateKey([]byte(opts.SSHKey))
	if err != nil {
		return nil, optionError(GitOptionSSHKey, "SSHKey is not an unencrypted PEM private key")
	}
	kh, err := parseKnownHosts(opts.KnownHosts)
	if err != nil {
		return nil, &GitOptionError{Option: GitOptionKnownHosts, err: err}
	}
	port := u.Port()
	if port == "" {
		port = strconv.Itoa(gitssh.DefaultPort)
	}
	user := u.User.Username()
	if user == "" {
		user = sshUser
	}
	return &sshAuth{cfg: ssh.ClientConfig{
		User:              user,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback:   kh.check,
		HostKeyAlgorithms: kh.algorithms(net.JoinHostPort(u.Hostname(), port)),
		Timeout:           30 * time.Second,
	}}, nil
}

// sshAuth offers one key and checks the host key against the given known
// hosts. go-git falls back to ~/.ssh/known_hosts only when HostKeyCallback is
// nil, and here it never is; it asks an SSH agent only when no AuthMethod is
// set, and here one always is.
type sshAuth struct{ cfg ssh.ClientConfig }

func (a *sshAuth) Name() string   { return gitssh.PublicKeysName }
func (a *sshAuth) String() string { return gitssh.PublicKeysName }

// ClientConfig returns a copy, so go-git's changes to it do not stick.
func (a *sshAuth) ClientConfig() (*ssh.ClientConfig, error) {
	cfg := a.cfg
	return &cfg, nil
}

// Revision returns the commit SHA the last successful Load read, or "" before
// one.
func (s *GitSource) Revision() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

// Load returns the Snapshot of the ref's current commit, or the cached one
// when the ref has not moved. An invalid file fails the load, named relative
// to the repository root. Cancelling parent stops a fetch in flight, and a
// Load whose parent has already ended fetches nothing; the Source's own bound
// applies inside parent either way.
func (s *GitSource) Load(parent context.Context) (*Snapshot, error) {
	s.load.Lock()
	defer s.load.Unlock()
	if parent.Err() != nil {
		return nil, errGitLoadCancelled()
	}
	budget := &gitBudget{wire: s.maxWire, mem: s.maxMem}
	ctx, cancel := context.WithTimeout(context.WithValue(parent, gitBudgetKey{}, budget), s.timeout)
	defer cancel()
	var dial transport.ProxyOptions
	if _, ok := s.auth.(*sshAuth); ok {
		deadline, _ := ctx.Deadline()
		var forget func()
		dial, forget = routeSSH(&sshDialer{budget: budget, deadline: deadline, load: ctx})
		defer forget()
	}

	remote := git.NewRemote(newLoadStorage(ctx, budget), &config.RemoteConfig{Name: "origin", URLs: []string{s.url}})
	refs, err := remote.ListContext(ctx, &git.ListOptions{Auth: s.auth, CABundle: s.caBundle, ProxyOptions: dial})
	if err != nil {
		return nil, s.fetchError(parent, err, budget)
	}
	name, hash, err := s.pickRef(refs)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	cached := s.snap
	if cached != nil && hash != s.refHash {
		cached = nil
	}
	s.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	clone := &git.CloneOptions{
		URL: s.url, Auth: s.auth, CABundle: s.caBundle, ProxyOptions: dial,
		ReferenceName: name, SingleBranch: true, Tags: git.NoTags,
	}
	if s.shallow {
		clone.Depth = 1
	}
	repo, err := git.CloneContext(ctx, newLoadStorage(ctx, budget), nil, clone)
	if err != nil {
		return nil, s.fetchError(parent, err, budget)
	}
	commit, err := headCommit(repo)
	if err != nil {
		return nil, errors.New("git source: the ref does not point to a commit")
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, errors.New("git source: cannot read the commit's tree")
	}
	if s.path != "" {
		if !fs.ValidPath(s.path) || len(s.path) > maxTreePathLen {
			return nil, ErrGitPath
		}
		if tree, err = tree.Tree(s.path); err != nil {
			return nil, ErrGitPath
		}
	}
	snap, err := loadTree(tree)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.snap, s.refHash, s.rev = snap, hash, commit.Hash.String()
	s.mu.Unlock()
	return snap, nil
}

// fullRef turns a short ref into the branch name it most likely means, for
// validation only.
func fullRef(ref string) string {
	if strings.HasPrefix(ref, "refs/") {
		return ref
	}
	return "refs/heads/" + ref
}

// pickRef finds the configured ref among the remote's: a full name as is, a
// short name as a branch first and a tag second, and an empty one as HEAD.
func (s *GitSource) pickRef(refs []*plumbing.Reference) (plumbing.ReferenceName, plumbing.Hash, error) {
	byName := make(map[plumbing.ReferenceName]*plumbing.Reference, len(refs))
	for _, r := range refs {
		byName[r.Name()] = r
	}
	var candidates []plumbing.ReferenceName
	switch {
	case s.ref == "":
		head := byName[plumbing.HEAD]
		if head != nil && head.Type() == plumbing.HashReference {
			return plumbing.HEAD, head.Hash(), nil
		}
		if head != nil {
			candidates = []plumbing.ReferenceName{head.Target()}
		}
	case strings.HasPrefix(s.ref, "refs/"):
		candidates = []plumbing.ReferenceName{plumbing.ReferenceName(s.ref)}
	default:
		candidates = []plumbing.ReferenceName{plumbing.NewBranchReferenceName(s.ref), plumbing.NewTagReferenceName(s.ref)}
	}
	for _, c := range candidates {
		if r := byName[c]; r != nil && r.Type() == plumbing.HashReference {
			return c, r.Hash(), nil
		}
	}
	if s.ref == "" {
		return "", plumbing.ZeroHash, errors.New("git source: the remote HEAD names no branch; set Ref")
	}
	return "", plumbing.ZeroHash, fmt.Errorf("git source: ref %q not found", s.ref)
}

// headCommit returns the commit HEAD names, peeling an annotated tag.
func headCommit(repo *git.Repository) (*object.Commit, error) {
	head, err := repo.Head()
	if err != nil {
		return nil, err
	}
	if c, err := repo.CommitObject(head.Hash()); err == nil {
		return c, nil
	}
	tag, err := repo.TagObject(head.Hash())
	if err != nil {
		return nil, err
	}
	return tag.Commit()
}

// errGitLoadCancelled is the fixed error of a Load whose parent ended, before
// or during the fetch. Both paths build it here, so its text cannot drift; it
// wraps nothing and names no URL, address or path.
func errGitLoadCancelled() error {
	return errors.New("git source: " + errLoadCancelled.Error())
}

// fetchError maps a transport error to a fixed message. The library's own
// text can carry the URL, user info, addresses or paths, so it is never
// passed on. A Load stopped by its budget, or by the end of parent, says so,
// whatever go-git wrapped.
func (s *GitSource) fetchError(parent context.Context, err error, budget *gitBudget) error {
	var (
		certErr  *tls.CertificateVerificationError
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
		invalid  x509.CertificateInvalidError
		opErr    *net.OpError
	)
	msg := "fetch failed"
	switch {
	case budget.exceeded():
		msg = "the repository " + errLoadLimit.Error()
	case parent.Err() != nil:
		return errGitLoadCancelled()
	case errors.Is(err, errHostKeyNotTrusted),
		// The server has no key of a type KnownHosts lists for it; x/crypto
		// has no error type for that, only this text.
		strings.Contains(err.Error(), "ssh: no common algorithm for host key"):
		msg = "the SSH host key is not trusted by KnownHosts"
	case errors.Is(err, transport.ErrAuthenticationRequired), errors.Is(err, transport.ErrAuthorizationFailed):
		msg = "authentication failed; check the token"
	case strings.Contains(err.Error(), "ssh: unable to authenticate"):
		// x/crypto has no error type for a refused key, only this text.
		msg = "authentication failed; check the SSH key"
	case errors.Is(err, transport.ErrRepositoryNotFound):
		msg = "repository not found, or not readable with these credentials"
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		msg = "the repository is empty"
	case errors.As(err, &certErr), errors.As(err, &unknown), errors.As(err, &hostname), errors.As(err, &invalid):
		msg = "the server's TLS certificate is not trusted"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		msg = "timed out after " + s.timeout.String()
	case errors.As(err, &opErr):
		msg = "cannot connect to the git server"
	}
	return errors.New("git source: " + msg)
}
