// Package engine owns Items, Kinds, validation, digests, keyword search,
// Resolution, Sources and Snapshots. It is pure: no HTTP, and it imports no
// other goldie module.
//
// The tracer slice covers global articles: a DirSource loads
// global/articles/** from a directory into an immutable Snapshot, and
// Snapshot.Search ranks them with the keyword scoring of ADR 0005.
package engine
