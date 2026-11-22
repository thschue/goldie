// Package engine owns Items, Kinds, validation, digests, keyword search,
// Resolution, Sources and Snapshots. It is pure: it serves no HTTP, owns no
// inbound transport (that is the API's job) and imports no other goldie
// module. A Source may still be a client of a remote, for example by cloning a
// git repository.
//
// A DirSource loads global/articles/** and
// projects/<host>/<org>/<repo>/articles/** from a directory into an immutable
// Snapshot. Every Source shares one loader over an fs.FS: DirSource hands it
// the os.Root-confined directory, and a read-only fs.FS over a git tree lets a
// commit load the same way without a working tree. That tree fs.FS never
// follows a symlink, rejects entry names that could traverse and trees with
// two entries of one name, and caps each load's steps, content bytes and path
// length, so a tree that reuses subtrees cannot expand without bound.
//
// A GitSource loads a git repository over https, ssh or file through that
// tree fs.FS. It uses go-git in process (no git binary) and clones into memory
// (no disk), depth 1 over https and ssh. Each Load lists the remote refs first
// and clones only when the ref moved; otherwise it returns the cached
// Snapshot. Its revision is the commit SHA, and one Load is bounded by two
// minutes, unpacking included. A remote is untrusted, so each Load also has a
// byte budget: what it reads from an https or ssh server, ref listing
// included, and the pack plus its objects once unpacked, charged from the
// object headers before anything is unpacked. Each object is charged 1024
// bytes on top of its size, above the about 540 live bytes per object a pack
// of tiny objects was measured to peak at; go-git's delta-base cache (at most
// 96 MiB of copies of charged objects) is not charged again. A load over
// either budget stops there with a fixed error.
// NewGitSource installs, once per process, go-git's in-process file:// server,
// an https client that charges the budget, and an ssh dialer that charges it
// and ends each connection at the Load's bound.
//
// Credentials are values. A token is accepted only with https and sent as the
// basic-auth password. An ssh URL needs an unencrypted PEM private key and
// known_hosts content; its user, git by default, is the login. The host key
// is always verified from that content in memory: plain and hashed hosts,
// [host]:port, negation and @revoked, while wildcards, @cert-authority and
// host certificates fail closed. No ~/.ssh file and no SSH agent is read. A password in the URL,
// any other URL user, a query string or a fragment, http:// and git:// are
// rejected. Errors are fixed
// messages that never carry the URL, a credential, a key fingerprint,
// known_hosts content or a host path.
//
// Search and Article see global articles only; SearchIn and ArticleIn take a
// Project and resolve its articles before global ones, ranking with the keyword
// scoring of ADR 0005.
//
// A Syncer polls one Source and serves its latest good Snapshot. A successful
// load swaps the Snapshot atomically for concurrent readers; a failed or
// panicking load keeps the previous one. Status reports the Source's opaque
// revision, the poll times and the last error, redacted of paths, URLs,
// addresses and credentials.
package engine
