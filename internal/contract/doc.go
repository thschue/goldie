// Package contract holds the OpenAPI document and the Go types and strict
// net/http server interface generated from it (contract.gen.go, written by
// `./run regen`, never edited by hand; `./run check` fails on drift). It also
// holds what every API client shares: NewAPIClient, the hardened client (no
// redirects, a 16 MiB answer cap, X-Goldie-Client), CleanText and CleanLine
// for untrusted server text, and ParseOK and DecodeAPIError for the error
// envelope. Its subpackages hold other shared helpers: contract/project detects
// the Project of a working directory for the CLI and MCP, contract/contracttest
// checks responses against the document and builds example request paths from it.
//
// The hand-written helpers sit beside the generated code: reference.go renders
// the endpoint reference Markdown from the document, and CheckArticleID
// (articleid.go) rejects article ids that URL path cleaning would resolve away. The package
// imports no other goldie module.
package contract
