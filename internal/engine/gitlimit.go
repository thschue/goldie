package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"golang.org/x/net/proxy"
)

// Load limits for one GitSource Load. The remote is untrusted, and a pack is
// compressed and delta encoded, so a few MB on the wire can unpack to GB.
const (
	maxGitWireBytes   = 64 << 20  // bytes read from an https or ssh server, ref listing included
	maxGitMemoryBytes = 256 << 20 // the pack plus its objects once unpacked
	gitObjectOverhead = 256       // charged per object for its bookkeeping
)

// gitBudget is what one Load may still read and unpack. hit records that a
// limit stopped it, whatever error go-git wraps that in.
type gitBudget struct {
	mu   sync.Mutex
	wire int64
	mem  int64
	hit  bool
}

func (b *gitBudget) spend(left *int64, n int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 0 || n > *left {
		*left, b.hit = 0, true
		return errLoadLimit
	}
	*left -= n
	return nil
}

func (b *gitBudget) exceeded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hit
}

// gitBudgetKey carries a Load's budget to the connections it dials.
type gitBudgetKey struct{}

// newGitHTTPClient is go-git's https client with each connection's reads
// charged to the budget of the Load that dialled it. It stays an
// *http.Transport, so go-git can still apply a CA bundle to a clone of it, and
// keep-alives are off, so no connection outlives its Load.
func newGitHTTPClient() transport.Transport {
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			if b, ok := ctx.Value(gitBudgetKey{}).(*gitBudget); ok {
				return budgetConn{Conn: c, b: b}, nil
			}
			return c, nil
		},
	}
	return githttp.NewClient(&http.Client{Transport: tr})
}

// sshDialScheme names the dialer through which every ssh connection of a Load
// goes. go-git dials ssh with no context and leaves the connection without a
// deadline, so a server that stalls in the handshake would hold the Load past
// its bound; its proxy hook is the only place that sees the connection.
const sshDialScheme = "goldie-ssh-load"

var (
	sshLoads   sync.Map // load id -> *sshDialer
	sshLoadSeq atomic.Uint64
)

// registerSSHDialer installs the dialer type behind sshDialScheme, once, from
// useGitTransports.
func registerSSHDialer() {
	proxy.RegisterDialerType(sshDialScheme, func(u *url.URL, _ proxy.Dialer) (proxy.Dialer, error) {
		d, ok := sshLoads.Load(u.Host)
		if !ok {
			return nil, errors.New("no load for this ssh connection")
		}
		return d.(*sshDialer), nil
	})
}

// sshDialer dials a Load's ssh connections, through ALL_PROXY when it is set,
// as go-git would. Each connection ends at the Load's deadline and charges its
// reads to the Load's wire budget, like an https one.
type sshDialer struct {
	budget   *gitBudget
	deadline time.Time
}

// sshProxy routes the ssh connections of one Load through an sshDialer. The
// returned func forgets the Load.
func sshProxy(budget *gitBudget, deadline time.Time) (transport.ProxyOptions, func()) {
	id := strconv.FormatUint(sshLoadSeq.Add(1), 10)
	sshLoads.Store(id, &sshDialer{budget: budget, deadline: deadline})
	return transport.ProxyOptions{URL: sshDialScheme + "://" + id}, func() { sshLoads.Delete(id) }
}

// Dial always fails: go-git dials with DialContext, and a wrapper that drops
// the context would slip past the re-entry guard in dialVia.
func (d *sshDialer) Dial(_, _ string) (net.Conn, error) {
	return nil, errors.New("the git ssh load dialer needs a context")
}

func (d *sshDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.dialVia(ctx, proxy.FromEnvironmentUsing(&net.Dialer{Timeout: 30 * time.Second}), network, addr)
}

// sshDialKey marks a context that is already inside a load dialer.
type sshDialKey struct{}

// dialVia dials through fwd. ALL_PROXY resolves through the same registry, so
// it could name a load dialer, alone or wrapped (x/net wraps it in a PerHost
// when NO_PROXY is set). Coming back in would recurse, so it fails instead.
func (d *sshDialer) dialVia(ctx context.Context, fwd proxy.Dialer, network, addr string) (net.Conn, error) {
	if _, loop := fwd.(*sshDialer); loop || ctx.Value(sshDialKey{}) != nil {
		return nil, errors.New("ALL_PROXY names a git ssh load dialer")
	}
	ctx, cancel := context.WithDeadline(context.WithValue(ctx, sshDialKey{}, true), d.deadline)
	defer cancel()
	var (
		c   net.Conn
		err error
	)
	switch fwd := fwd.(type) {
	case proxy.ContextDialer:
		c, err = fwd.DialContext(ctx, network, addr)
	default:
		c, err = fwd.Dial(network, addr)
	}
	if err != nil {
		return nil, err
	}
	if err := c.SetDeadline(d.deadline); err != nil {
		_ = c.Close()
		return nil, err
	}
	return budgetConn{Conn: c, b: d.budget}, nil
}

// budgetConn charges what it reads to the wire budget.
type budgetConn struct {
	net.Conn
	b *gitBudget
}

func (c budgetConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		if berr := c.b.spend(&c.b.wire, int64(n)); berr != nil {
			return 0, berr
		}
	}
	return n, err
}

// budgetStorage is go-git's in-memory store with a pack writer, so a fetch
// hands it the raw pack and the pack is unpacked only once it fits the budget.
type budgetStorage struct {
	*memory.Storage
	b *gitBudget
}

func newBudgetStorage(b *gitBudget) budgetStorage {
	return budgetStorage{Storage: memory.NewStorage(), b: b}
}

// PackfileWriter implements storer.PackfileWriter.
func (s budgetStorage) PackfileWriter() (io.WriteCloser, error) {
	return &packWriter{s: s}, nil
}

// packWriter buffers a pack, charging each byte to the memory budget, so the
// transfer stops at the limit. Close unpacks it after checkPack.
type packWriter struct {
	s   budgetStorage
	buf bytes.Buffer
	err error
}

func (w *packWriter) Write(p []byte) (int, error) {
	if w.err == nil {
		w.err = w.s.b.spend(&w.s.b.mem, int64(len(p)))
	}
	if w.err != nil {
		return 0, w.err
	}
	return w.buf.Write(p)
}

func (w *packWriter) Close() error {
	if w.err != nil {
		return w.err
	}
	pack := w.buf.Bytes()
	if err := checkPack(pack, w.s.b); err != nil {
		return err
	}
	p, err := packfile.NewParserWithStorage(packfile.NewScanner(bytes.NewReader(pack)), w.s.Storage)
	if err != nil {
		return err
	}
	_, err = p.Parse()
	return err
}

var errBadPack = errors.New("malformed pack")

// checkPack walks the object headers of pack before anything is unpacked and
// charges each object's unpacked size to the memory budget. A whole object's
// size is in its header, and go-git never inflates past it. A delta's result
// size is declared in its data, and go-git rejects a delta that writes more.
func checkPack(pack []byte, b *gitBudget) error {
	sc := packfile.NewScanner(bytes.NewReader(pack))
	_, n, err := sc.Header()
	if err != nil {
		return errBadPack
	}
	var delta bytes.Buffer
	for range n {
		oh, err := sc.NextObjectHeader()
		if err != nil {
			return errBadPack
		}
		if err := b.spend(&b.mem, oh.Length+gitObjectOverhead); err != nil {
			return err
		}
		if !oh.Type.IsDelta() {
			continue
		}
		delta.Reset()
		if _, _, err := sc.NextObject(&delta); err != nil {
			return errBadPack
		}
		target, err := deltaTargetSize(delta.Bytes())
		if err != nil {
			return err
		}
		if err := b.spend(&b.mem, target); err != nil {
			return err
		}
	}
	return nil
}

// deltaTargetSize reads the result size from a delta's header: two
// little-endian base-128 numbers, the source size and then the target size.
func deltaTargetSize(d []byte) (int64, error) {
	var size uint64
	for range 2 {
		size = 0
		for shift := uint(0); ; shift += 7 {
			if len(d) == 0 || shift > 63 {
				return 0, errBadPack
			}
			c := d[0]
			d = d[1:]
			size |= uint64(c&0x7f) << shift
			if c&0x80 == 0 {
				break
			}
		}
	}
	if size > 1<<62 {
		return -1, nil // spend rejects a negative size
	}
	return int64(size), nil
}
