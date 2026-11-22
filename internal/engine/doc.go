// Package engine owns Items, Kinds, validation, digests, keyword search,
// Resolution, Sources and Snapshots. It is pure: no HTTP, and it imports no
// other goldie module.
//
// A DirSource loads global/articles/** and
// projects/<host>/<org>/<repo>/articles/** from a directory into an immutable
// Snapshot. Search and Article see global articles only; SearchIn and ArticleIn
// take a Project and resolve its articles before global ones, ranking with the
// keyword scoring of ADR 0005.
//
// A GitSource reads the same layout from a git repository through the same
// loader, over an fs.FS of the commit's tree. It clones into memory with
// go-git (no git binary, no disk), lists the ref first and clones only when it
// moved, and reports the commit SHA as its revision. Credentials are values: a
// token over https only, or an SSH key whose server host key is always checked
// against the supplied known hosts. Symlinks in the tree are never followed,
// and errors never carry the URL or a credential. The engine serves no HTTP;
// the git Source is only an HTTPS or SSH client of the remote.
//
// A Syncer polls one Source and serves its latest good Snapshot. A successful
// load swaps the Snapshot atomically for concurrent readers; a failed or
// panicking load keeps the previous one. Status reports the Source's opaque
// revision, the poll times and the last error, redacted of paths, URLs,
// addresses and credentials.
package engine
