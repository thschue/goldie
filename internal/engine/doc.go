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
