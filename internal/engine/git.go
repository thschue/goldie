package engine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
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
)

// GitOptions configures a git Source. Credentials are passed in as values: the
// engine reads no environment or file for them.
type GitOptions struct {
	// URL is https://host/path or file:///path. It carries no credentials;
	// those go in Token.
	URL string
	// Ref is a branch, a tag or a full ref name (refs/...). Empty means the
	// remote's HEAD.
	Ref string
	// Token is an HTTPS access token, sent as the basic-auth password. It is
	// only accepted with an https URL.
	Token string
}

// String describes o without the token or URL user info, so logging
// GitOptions with %v or %+v never prints a credential.
func (o GitOptions) String() string {
	return fmt.Sprintf("{URL:%s Ref:%q Token:%s}", redactURL(o.URL), o.Ref, redactToken(o.Token))
}

// GoString is String, for %#v.
func (o GitOptions) GoString() string { return "engine.GitOptions" + o.String() }

// LogValue implements slog.LogValuer with the same redaction as String.
func (o GitOptions) LogValue() slog.Value {
	return slog.GroupValue(slog.String("url", redactURL(o.URL)), slog.String("ref", o.Ref),
		slog.String("token", redactToken(o.Token)))
}

func redactToken(t string) string {
	if t == "" {
		return ""
	}
	return "[redacted]"
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[invalid URL]"
	}
	if u.User != nil {
		u.User = url.User("[redacted]")
	}
	return u.String()
}

// gitLoadTimeout bounds one Load, so a stalled server cannot hold the Syncer.
const gitLoadTimeout = 2 * time.Minute

// tokenUser is the basic-auth user name sent with Token. GitHub and GitLab
// accept any non-empty name with an access token.
const tokenUser = "x-access-token"

var installGitTransports sync.Once

// useGitTransports installs, once per process, the go-git transports a
// GitSource needs. PC-11: no git binary, so file:// is served by go-git's
// in-process server rather than an exec'd git-upload-pack. https goes through
// a client that charges each Load's reads to its budget.
func useGitTransports() {
	installGitTransports.Do(func() {
		client.InstallProtocol("file", server.NewClient(server.DefaultLoader))
		client.InstallProtocol("https", newGitHTTPClient())
	})
}

// GitSource is a Source backed by a git repository. Each Load lists the remote
// refs; when the ref moved, Load clones that commit into memory (no working
// tree, no disk) and reads its tree through the loader DirSource uses.
type GitSource struct {
	url     string
	ref     string
	auth    transport.AuthMethod
	shallow bool
	// caBundle is an extra trusted CA in PEM, set only by tests.
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
// Its errors are fixed messages that never contain the URL or the token.
func NewGitSource(opts GitOptions) (*GitSource, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return nil, errors.New("git source: URL is empty")
	}
	u, err := url.Parse(opts.URL)
	if err != nil {
		return nil, errors.New("git source: URL is not valid")
	}
	if u.User != nil {
		return nil, errors.New("git source: URL must not carry credentials; use Token")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && opts.Token != "" {
		return nil, errors.New("git source: Token needs an HTTPS URL")
	}
	s := &GitSource{url: opts.URL, ref: opts.Ref, timeout: gitLoadTimeout, maxWire: maxGitWireBytes, maxMem: maxGitMemoryBytes}
	switch {
	case scheme == "https" && u.Host != "" && u.Opaque == "":
		s.shallow = true
		if opts.Token != "" {
			s.auth = &githttp.BasicAuth{Username: tokenUser, Password: opts.Token}
		}
	case scheme == "file" && u.Host == "" && u.Opaque == "" && u.Path != "":
		// go-git's in-process server does not serve shallow clones.
	default:
		return nil, errors.New("git source: URL must be HTTPS with a host, or FILE with an absolute path")
	}
	if _, err := transport.NewEndpoint(opts.URL); err != nil {
		return nil, errors.New("git source: URL is not valid")
	}
	if opts.Ref != "" && plumbing.ReferenceName(fullRef(opts.Ref)).Validate() != nil {
		return nil, fmt.Errorf("git source: ref %q is not a valid ref name", opts.Ref)
	}
	useGitTransports()
	return s, nil
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
// to the repository root.
func (s *GitSource) Load() (*Snapshot, error) {
	s.load.Lock()
	defer s.load.Unlock()
	budget := &gitBudget{wire: s.maxWire, mem: s.maxMem}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), gitBudgetKey{}, budget), s.timeout)
	defer cancel()

	remote := git.NewRemote(newBudgetStorage(budget), &config.RemoteConfig{Name: "origin", URLs: []string{s.url}})
	refs, err := remote.ListContext(ctx, &git.ListOptions{Auth: s.auth, CABundle: s.caBundle})
	if err != nil {
		return nil, s.fetchError(err, budget)
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
		URL: s.url, Auth: s.auth, CABundle: s.caBundle,
		ReferenceName: name, SingleBranch: true, Tags: git.NoTags,
	}
	if s.shallow {
		clone.Depth = 1
	}
	repo, err := git.CloneContext(ctx, newBudgetStorage(budget), nil, clone)
	if err != nil {
		return nil, s.fetchError(err, budget)
	}
	commit, err := headCommit(repo)
	if err != nil {
		return nil, errors.New("git source: the ref does not point to a commit")
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, errors.New("git source: cannot read the commit's tree")
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

// fetchError maps a transport error to a fixed message. The library's own
// text can carry the URL, user info, addresses or paths, so it is never
// passed on. A Load stopped by its budget says so, whatever go-git wrapped.
func (s *GitSource) fetchError(err error, budget *gitBudget) error {
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
	case errors.Is(err, transport.ErrAuthenticationRequired), errors.Is(err, transport.ErrAuthorizationFailed):
		msg = "authentication failed; check the token"
	case errors.Is(err, transport.ErrRepositoryNotFound):
		msg = "repository not found, or not readable with these credentials"
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		msg = "the repository is empty"
	case errors.As(err, &certErr), errors.As(err, &unknown), errors.As(err, &hostname), errors.As(err, &invalid):
		msg = "the server's TLS certificate is not trusted"
	case errors.Is(err, context.DeadlineExceeded):
		msg = "timed out after " + s.timeout.String()
	case errors.As(err, &opErr):
		msg = "cannot connect to the git server"
	}
	return errors.New("git source: " + msg)
}
