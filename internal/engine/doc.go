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
// A GitSource loads a git repository over https or file through that tree
// fs.FS. It uses go-git in process (no git binary) and clones into memory (no
// disk), depth 1 over https. Each Load lists the remote refs first and clones
// only when the ref moved; otherwise it returns the cached Snapshot. Its
// revision is the commit SHA, and one Load is bounded by two minutes. A remote
// is untrusted, so each Load also has a byte budget: what it reads from an
// https server, ref listing included, and the pack plus its objects once
// unpacked, charged from the object headers before anything is unpacked. A
// load over either stops there with a fixed error. NewGitSource installs, once
// per process, go-git's in-process file:// server and an https client that
// charges the budget. A token
// is a value, accepted only with https and sent as the basic-auth password;
// credentials in the URL, http:// and git:// are rejected. Its errors are
// fixed messages that never carry the URL, the token or a host path.
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
