package engine_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/thschue/goldie/internal/engine"
)

// hostCert signs a host certificate for host's key with ca, valid from after
// to before (Unix seconds).
func hostCert(t *testing.T, ca, host sshKey, after, before uint64) (*ssh.Certificate, ssh.Signer) {
	t.Helper()
	cert := &ssh.Certificate{
		Key:             host.pub,
		CertType:        ssh.HostCert,
		KeyId:           "goldie-test-host",
		ValidPrincipals: []string{"127.0.0.1"},
		ValidAfter:      after,
		ValidBefore:     before,
	}
	if err := cert.SignCert(rand.Reader, ca.signer); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewCertSigner(cert, host.signer)
	if err != nil {
		t.Fatal(err)
	}
	return cert, signer
}

// TestGitSourceSSHHostCertificateOnAPlainLineFailsClosed lists a host
// certificate blob on an unmarked known_hosts line. It is not a host key:
// whether it is valid, expired or for a revoked key, the load fails with the
// fixed host key error before the client offers its key.
func TestGitSourceSSHHostCertificateOnAPlainLineFailsClosed(t *testing.T) {
	client := newSSHKey(t)
	other := newSSHKey(t)
	ca := newSSHKey(t)
	host := newSSHKey(t)
	past := uint64(time.Now().Add(-time.Hour).Unix())
	valid, validSigner := hostCert(t, ca, host, 0, ssh.CertTimeInfinity)
	expired, expiredSigner := hostCert(t, ca, host, 0, past)
	unrelated := "\ngit.example.invalid " + authorizedKey(other.pub)

	for _, tc := range []struct {
		name   string
		signer ssh.Signer
		kh     func(srv *sshServer) string
	}{
		{"certificate", validSigner, func(srv *sshServer) string {
			return srv.pattern() + " " + authorizedKey(valid) + unrelated
		}},
		{"expired certificate", expiredSigner, func(srv *sshServer) string {
			return srv.pattern() + " " + authorizedKey(expired) + unrelated
		}},
		{"certificate for a revoked key", validSigner, func(srv *sshServer) string {
			return srv.pattern() + " " + authorizedKey(valid) + "\n@revoked * " + authorizedKey(host.pub) + unrelated
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newSSHServer(t, client.pub, tc.signer)
			kh := tc.kh(srv)
			_, err := newGit(t, engine.GitOptions{URL: srv.url("git"), SSHKey: client.pem, KnownHosts: kh}).Load(t.Context())
			if err == nil || !strings.Contains(err.Error(), "host key") {
				t.Fatalf("err = %v, want a host key failure", err)
			}
			assertFixedGitError(t, err)
			if o, l := srv.seen(); o != 0 || l != 0 {
				t.Fatal("the client trusted a host certificate and offered its key")
			}
			assertNoKeyMaterial(t, err.Error(), append(append(client.material(), ca.material()...), knownHostsMaterial(kh)...)...)
		})
	}
}

// TestGitSourceSSHNoListedHostKeyTypeIsAHostKeyError serves only an ed25519
// host key while KnownHosts lists an ECDSA key for the host: the handshake
// finds no common host key type, and that is the fixed host key error.
func TestGitSourceSSHNoListedHostKeyTypeIsAHostKeyError(t *testing.T) {
	client := newSSHKey(t)
	srv := newSSHServer(t, client.pub)
	ecPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	kh := srv.pattern() + " " + authorizedKey(sshKeyFrom(t, ecPriv).pub)
	_, err = newGit(t, engine.GitOptions{URL: srv.url("git"), SSHKey: client.pem, KnownHosts: kh}).Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), "host key") {
		t.Fatalf("err = %v, want a host key failure", err)
	}
	assertFixedGitError(t, err)
	if o, l := srv.seen(); o != 0 || l != 0 {
		t.Fatal("the client offered its key without a trusted host key")
	}
	assertNoKeyMaterial(t, err.Error(), append(append(client.material(), srv.hostMaterial()...), knownHostsMaterial(kh)...)...)
}

// TestGitSourceSSHReadsCountAgainstTheWireLimit lowers the wire budget below
// what an SSH handshake reads: an ssh load stops with the fixed load limit
// error, like an https one.
func TestGitSourceSSHReadsCountAgainstTheWireLimit(t *testing.T) {
	client := newSSHKey(t)
	srv := newSSHServer(t, client.pub)
	src := newGit(t, engine.GitOptions{URL: srv.url("git"), SSHKey: client.pem, KnownHosts: srv.line()})
	engine.SetGitLoadLimits(src, 64, 64<<20)
	_, err := src.Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), "exceeds the load limits") {
		t.Fatalf("err = %v, want the load limit error", err)
	}
	assertFixedGitError(t, err)
	if _, l := srv.seen(); l != 0 {
		t.Fatal("logged in past the wire limit")
	}
}
