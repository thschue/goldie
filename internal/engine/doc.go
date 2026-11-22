// Package engine owns Items, Kinds, validation, digests, keyword search,
// Resolution, Sources and Snapshots. It is pure: no HTTP, and it imports no
// other goldie module.
//
// A DirSource loads global/articles/** and
// projects/<host>/<org>/<repo>/articles/** from a directory into an immutable
// Snapshot. Search and Article see global articles only; SearchIn and ArticleIn
// take a Project and resolve its articles before global ones, ranking with the
// keyword scoring of ADR 0005.
package engine
