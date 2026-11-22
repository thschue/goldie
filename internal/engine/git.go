package engine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
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
	"github.com/go-git/go-git/v5/storage/memory"
	"golang.org/x/crypto/ssh"
)

// GitOptions configures a git Source. Credentials are passed in as values: the
// engine reads no environment, file or SSH agent for them.
type GitOptions struct {
	// URL is https://..., ssh://user@host/path, user@host:path or file://...
	// It carries no password; credentials go in Token or SSHKey.
	URL string
	// Ref is a branch, a tag or a full ref name (refs/...). Empty means the
	// remote's HEAD.
	Ref string
	// Token is an HTTPS access token, sent as the basic-auth password. It is
	// only accepted with an https URL.
	Token string
	// SSHKey is an unencrypted PEM private key for an ssh URL.
	SSHKey string
	// KnownHosts is known_hosts content. An ssh URL needs it: the server's host
	// key is always checked against it, and there is no way to skip that.
	KnownHosts string
}

// gitTimeout bounds one Load, so a stalled server cannot hold the Syncer.
const gitTimeout = 2 * time.Minute

// tokenUser is the basic-auth user name sent with Token. GitHub and GitLab
// accept any non-empty name with an access token.
const tokenUser = "x-access-token"

func init() {
	// PC-11: no git binary. go-git's file transport would exec git-upload-pack,
	// so serve file:// with its in-process server instead. And read no
	// ~/.ssh/config, which could redirect a host.
	client.InstallProtocol("file", server.NewClient(server.DefaultLoader))
	gitssh.DefaultSSHConfig = nil
}

var _ interface {
	Source
	Revisioner
} = (*GitSource)(nil)

// GitSource is a Source backed by a git repository. Each Load lists the remote
// ref; when it moved, Load clones that commit into memory (no working tree, no
// disk) and reads its tree through the same loader as DirSource.
type GitSource struct {
	url     string
	ref     string
	auth    transport.AuthMethod
	shallow bool
	// caBundle is an extra trusted CA, set only by tests.
	caBundle []byte

	mu      sync.Mutex
	refHash plumbing.Hash // remote ref hash the cached Snapshot was read at
	rev     string        // commit SHA of the cached Snapshot
	snap    *Snapshot
}

// NewGitSource validates opts and returns a git Source. It contacts no server.
// Its errors never contain the URL or a credential.
func NewGitSource(opts GitOptions) (*GitSource, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return nil, errors.New("git source: URL is empty")
	}
	ep, err := transport.NewEndpoint(opts.URL)
	if err != nil {
		return nil, errors.New("git source: URL is not valid")
	}
	if opts.Ref != "" && plumbing.ReferenceName(fullRef(opts.Ref)).Validate() != nil {
		return nil, fmt.Errorf("git source: ref %q is not a valid ref name", opts.Ref)
	}
	s := &GitSource{url: opts.URL, ref: opts.Ref}
	if ep.Password != "" || (ep.Protocol != "ssh" && ep.User != "") {
		return nil, errors.New("git source: URL must not carry credentials; use Token or SSHKey")
	}
	if ep.Protocol != "ssh" && (opts.SSHKey != "" || opts.KnownHosts != "") {
		return nil, errors.New("git source: SSHKey and KnownHosts need an ssh URL")
	}
	if ep.Protocol != "https" && opts.Token != "" {
		return nil, errors.New("git source: Token needs an https URL")
	}
	switch ep.Protocol {
	case "https":
		s.shallow = true
		if opts.Token != "" {
			s.auth = &githttp.BasicAuth{Username: tokenUser, Password: opts.Token}
		}
	case "ssh":
		s.shallow = true
		if s.auth, err = newSSHAuth(ep, opts); err != nil {
			return nil, err
		}
	case "file":
		// go-git's in-process server does not serve shallow clones.
	default:
		return nil, errors.New("git source: URL scheme must be https, ssh or file")
	}
	return s, nil
}

func newSSHAuth(ep *transport.Endpoint, opts GitOptions) (*sshAuth, error) {
	if opts.SSHKey == "" {
		return nil, errors.New("git source: an ssh URL needs SSHKey")
	}
	if strings.TrimSpace(opts.KnownHosts) == "" {
		return nil, errors.New("git source: an ssh URL needs KnownHosts; host keys are always verified")
	}
	signer, err := ssh.ParsePrivateKey([]byte(opts.SSHKey))
	if err != nil {
		return nil, errors.New("git source: SSHKey is not an unencrypted PEM private key")
	}
	kh, err := parseKnownHosts(opts.KnownHosts)
	if err != nil {
		return nil, err
	}
	port := ep.Port
	if port <= 0 {
		port = gitssh.DefaultPort
	}
	user := ep.User
	if user == "" {
		user = gitssh.DefaultUsername
	}
	return &sshAuth{
		user:   user,
		signer: signer,
		hosts:  kh,
		algos:  kh.algorithms(net.JoinHostPort(ep.Host, strconv.Itoa(port))),
	}, nil
}

// sshAuth authenticates with one key and always verifies the host key against
// the supplied known hosts. go-git falls back to ~/.ssh/known_hosts only when
// HostKeyCallback is nil, and ClientConfig never returns nil.
type sshAuth struct {
	user   string
	signer ssh.Signer
	hosts  knownHosts
	algos  []string
}

func (a *sshAuth) Name() string   { return gitssh.PublicKeysName }
func (a *sshAuth) String() string { return gitssh.PublicKeysName }

func (a *sshAuth) ClientConfig() (*ssh.ClientConfig, error) {
	return &ssh.ClientConfig{
		User:              a.user,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(a.signer)},
		HostKeyCallback:   a.hosts.check,
		HostKeyAlgorithms: a.algos,
		Timeout:           30 * time.Second,
	}, nil
}

// Revision returns the commit SHA the last successful Load read.
func (s *GitSource) Revision() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

// Load returns the Snapshot of the ref's current commit. An invalid file fails
// the load, named relative to the repository root.
func (s *GitSource) Load() (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{s.url}})
	refs, err := remote.ListContext(ctx, &git.ListOptions{Auth: s.auth, CABundle: s.caBundle})
	if err != nil {
		return nil, fetchError(err)
	}
	name, hash, err := s.pickRef(refs)
	if err != nil {
		return nil, err
	}
	if s.snap != nil && hash == s.refHash {
		return s.snap, nil
	}
	clone := &git.CloneOptions{
		URL: s.url, Auth: s.auth, CABundle: s.caBundle,
		ReferenceName: name, SingleBranch: true, Tags: git.NoTags,
	}
	if s.shallow {
		clone.Depth = 1
	}
	repo, err := git.CloneContext(ctx, memory.NewStorage(), nil, clone)
	if err != nil {
		return nil, fetchError(err)
	}
	commit, err := headCommit(repo)
	if err != nil {
		return nil, errors.New("git source: the ref does not point to a commit")
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, errors.New("git source: cannot read the commit's tree")
	}
	snap, err := loadFS(newTreeFS(tree))
	if err != nil {
		return nil, err
	}
	s.snap, s.refHash, s.rev = snap, hash, commit.Hash.String()
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
		if head == nil {
			return "", plumbing.ZeroHash, errors.New("git source: the remote has no HEAD; set Ref")
		}
		if head.Type() == plumbing.HashReference {
			return plumbing.HEAD, head.Hash(), nil
		}
		candidates = []plumbing.ReferenceName{head.Target()}
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
		return "", plumbing.ZeroHash, errors.New("git source: the remote HEAD points to no branch; set Ref")
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
// text can carry the URL, user info or addresses, so it is never passed on.
func fetchError(err error) error {
	var (
		certErr  *tls.CertificateVerificationError
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
		invalid  x509.CertificateInvalidError
		opErr    *net.OpError
	)
	msg := "fetch failed"
	switch {
	case errors.Is(err, errHostKeyNotTrusted):
		msg = "the SSH host key does not match KnownHosts"
	case errors.Is(err, transport.ErrAuthenticationRequired), errors.Is(err, transport.ErrAuthorizationFailed),
		strings.Contains(err.Error(), "ssh: unable to authenticate"):
		msg = "authentication failed; check the token or SSH key"
	case errors.Is(err, transport.ErrRepositoryNotFound):
		msg = "repository not found, or not readable with these credentials"
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		msg = "the repository is empty"
	case errors.As(err, &certErr), errors.As(err, &unknown), errors.As(err, &hostname), errors.As(err, &invalid):
		msg = "the server's TLS certificate is not trusted"
	case errors.Is(err, context.DeadlineExceeded):
		msg = "timed out after " + gitTimeout.String()
	case errors.As(err, &opErr):
		msg = "cannot connect to the git server"
	}
	return errors.New("git source: " + msg)
}
