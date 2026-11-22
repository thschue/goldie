// Package engine owns Items, Kinds, validation, digests, keyword search,
// Resolution, Sources and Snapshots. It is pure: it serves no HTTP, has no
// transport layer and imports no other goldie module. That does not stop a
// Source from fetching its content, for example by cloning a git repository.
//
// A DirSource loads global/articles/** and
// projects/<host>/<org>/<repo>/articles/** from a directory into an immutable
// Snapshot. Every Source shares one loader over an fs.FS: DirSource hands it
// the os.Root-confined directory, and a read-only fs.FS over a git tree lets a
// commit load the same way without a working tree. That tree fs.FS never
// follows a symlink and rejects entry names that could traverse.
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
