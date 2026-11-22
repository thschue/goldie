// Package contract holds the OpenAPI document and the Go types and strict
// net/http server interface generated from it (contract.gen.go, written by
// `./run regen`, never edited by hand; `./run check` fails on drift). It also
// holds what every API client shares: CleanText and CleanLine for untrusted
// server text, and DecodeAPIError for the error envelope. It imports no other
// goldie module.
package contract
