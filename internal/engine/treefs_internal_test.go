package engine

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"golang.org/x/crypto/ssh"
)

// treeBuilder writes raw git objects, so a test can build trees no well-behaved
// client would produce.
type treeBuilder struct {
	t   *testing.T
	sto *memory.Storage
}

func (b treeBuilder) blob(content string) plumbing.Hash {
	b.t.Helper()
	obj := b.sto.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, _ := obj.Writer()
	_, _ = w.Write([]byte(content))
	_ = w.Close()
	h, err := b.sto.SetEncodedObject(obj)
	if err != nil {
		b.t.Fatal(err)
	}
	return h
}

// tree writes the raw tree format ("<mode> <name>\x00<hash>" per entry), in
// the order given and without the name checks go-git's encoder applies: a
// hostile remote is not bound by them either.
func (b treeBuilder) tree(entries ...object.TreeEntry) plumbing.Hash {
	b.t.Helper()
	obj := b.sto.NewEncodedObject()
	obj.SetType(plumbing.TreeObject)
	w, _ := obj.Writer()
	for _, e := range entries {
		_, _ = fmt.Fprintf(w, "%o %s\x00", uint32(e.Mode), e.Name)
		_, _ = w.Write(e.Hash[:])
	}
	_ = w.Close()
	h, err := b.sto.SetEncodedObject(obj)
	if err != nil {
		b.t.Fatal(err)
	}
	return h
}

func (b treeBuilder) fs(root plumbing.Hash) treeFS {
	b.t.Helper()
	tree, err := object.GetTree(b.sto, root)
	if err != nil {
		b.t.Fatal(err)
	}
	return newTreeFS(tree)
}

func TestTreeFSConforms(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	md := b.blob("---\nname: A\n---\nbody")
	inner := b.tree(
		object.TreeEntry{Name: "a.md", Mode: filemode.Regular, Hash: md},
		object.TreeEntry{Name: "run.sh", Mode: filemode.Executable, Hash: md},
	)
	root := b.tree(
		object.TreeEntry{Name: "README", Mode: filemode.Regular, Hash: b.blob("hi")},
		object.TreeEntry{Name: "global", Mode: filemode.Dir, Hash: inner},
	)
	if err := fstest.TestFS(b.fs(root), "global/a.md", "global/run.sh", "README"); err != nil {
		t.Fatal(err)
	}
}

func TestTreeFSNeverFollowsSymlinks(t *testing.T) {
	b := treeBuilder{t, memory.NewStorage()}
	dir := b.tree(object.TreeEntry{Name: "a.md", Mode: filemode.Regular, Hash: b.blob("x")})
	root := b.tree(
		object.TreeEntry{Name: "dir", Mode: filemode.Dir, Hash: dir},
		object.TreeEntry{Name: "link", Mode: filemode.Symlink, Hash: b.blob("dir")},
		object.TreeEntry{Name: "sub", Mode: filemode.Submodule, Hash: plumbing.NewHash("0123456789012345678901234567890123456789")},
	)
	fsys := b.fs(root)
	for _, name := range []string{"link", "link/a.md", "sub"} {
		if f, err := fsys.Open(name); err == nil {
			_ = f.Close()
			t.Errorf("Open(%q) succeeded", name)
		}
	}
	if _, err := fs.Stat(fsys, "link"); err == nil {
		t.Error("Stat followed a symlink")
	}
	fi, err := fs.Lstat(fsys, "link")
	if err != nil || fi.Mode().Type() != fs.ModeSymlink {
		t.Fatalf("Lstat = %v, %v", fi, err)
	}
	if target, err := fs.ReadLink(fsys, "link"); err != nil || target != "dir" {
		t.Fatalf("ReadLink = %q, %v", target, err)
	}
	if _, err := fs.Stat(fsys, "link/a.md"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("path through a symlink: err = %v, want ErrInvalid", err)
	}
}

func TestTreeFSRejectsTraversingNames(t *testing.T) {
	for _, name := range []string{"..", ".", "a/b", "a\x00b", ""} {
		b := treeBuilder{t, memory.NewStorage()}
		evil := b.tree(object.TreeEntry{Name: "x.md", Mode: filemode.Regular, Hash: b.blob("---\nname: X\n---\n")})
		articles := b.tree(object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: evil})
		global := b.tree(object.TreeEntry{Name: "articles", Mode: filemode.Dir, Hash: articles})
		root := b.tree(object.TreeEntry{Name: "global", Mode: filemode.Dir, Hash: global})
		snap, err := loadFS(b.fs(root))
		if err == nil {
			t.Errorf("entry %q: loaded %d articles", name, len(snap.Articles()))
		}
	}
}

// FuzzValidEntryName checks that an accepted tree entry name is always a single,
// non-traversing path element.
func FuzzValidEntryName(f *testing.F) {
	for _, s := range []string{"a.md", "..", ".", "", "a/b", "a\x00", ".git", "...", "a\\b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if !validEntryName(name) {
			return
		}
		if !fs.ValidPath(name) || strings.Contains(name, "/") || name == "." || name == ".." {
			t.Fatalf("accepted %q", name)
		}
	})
}

func hostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func hashed(host string) string {
	salt := []byte("0123456789abcdefghij")
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(host))
	return "|1|" + base64.StdEncoding.EncodeToString(salt) + "|" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestKnownHostsCheck(t *testing.T) {
	key, other := hostKey(t), hostKey(t)
	line := func(hosts string, k ssh.PublicKey) string {
		return hosts + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
	}
	for _, tc := range []struct {
		name, content, host string
		ok                  bool
	}{
		{"plain", line("git.example.com", key), "git.example.com:22", true},
		{"case-insensitive", line("Git.Example.com", key), "git.example.com:22", true},
		{"port", line("[git.example.com]:2222", key), "git.example.com:2222", true},
		{"port must match", line("git.example.com", key), "git.example.com:2222", false},
		{"one of several", line("a.example.com,git.example.com", key), "git.example.com:22", true},
		{"hashed", line(hashed("git.example.com"), key), "git.example.com:22", true},
		{"hashed other host", line(hashed("evil.example.com"), key), "git.example.com:22", false},
		{"other key", line("git.example.com", other), "git.example.com:22", false},
		{"other host", line("evil.example.com", key), "git.example.com:22", false},
		{"wildcard never matches", line("*", key), "git.example.com:22", false},
		{"negated", line("git.example.com,!git.example.com", key), "git.example.com:22", false},
		{"revoked", line("git.example.com", key) + "\n@revoked " + line("other.example.com", key), "git.example.com:22", false},
		{"cert-authority ignored", line("other.example.com", other) + "\n@cert-authority " + line("git.example.com", key), "git.example.com:22", false},
		{"comments and blanks", "# comment\n\n" + line("git.example.com", key) + "\n", "git.example.com:22", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kh, err := parseKnownHosts(tc.content)
			if err != nil {
				t.Fatal(err)
			}
			err = kh.check(tc.host, &net.TCPAddr{}, key)
			if (err == nil) != tc.ok {
				t.Fatalf("check = %v, want ok=%v", err, tc.ok)
			}
			if err != nil && !errors.Is(err, errHostKeyNotTrusted) {
				t.Fatalf("err = %v", err)
			}
		})
	}

	for _, bad := range []string{"", "# only a comment", "host not-a-key", "@cert-authority " + line("h", key)} {
		if _, err := parseKnownHosts(bad); err == nil {
			t.Errorf("parseKnownHosts(%q) accepted", bad)
		}
	}
}

func TestKnownHostsAlgorithms(t *testing.T) {
	ed := hostKey(t)
	content := "git.example.com " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(ed)))
	kh, err := parseKnownHosts(content)
	if err != nil {
		t.Fatal(err)
	}
	if got := kh.algorithms("git.example.com:22"); len(got) != 1 || got[0] != ssh.KeyAlgoED25519 {
		t.Fatalf("algorithms = %v", got)
	}
	if got := kh.algorithms("other.example.com:22"); got != nil {
		t.Fatalf("algorithms for an unknown host = %v", got)
	}
}
