// Package contract holds the OpenAPI document and the Go types and strict
// net/http server interface generated from it (contract.gen.go, written by
// `./run regen`, never edited by hand; `./run check` fails on drift). It also
// holds what every API client shares: NewAPIClient, the hardened client (no
// redirects, a 16 MiB answer cap, X-Goldie-Client), CleanText and CleanLine
// for untrusted server text, and ParseOK and DecodeAPIError for the error
// envelope. Its subpackages hold other shared helpers: contract/project detects
// the Project of a working directory for the CLI and MCP, contract/contracttest
// checks responses against the document. It imports no other goldie module.
package contract
