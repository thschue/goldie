// Package mcp is the MCP component. It reaches the API only through the
// contract client, never through the engine or api packages.
//
// New returns a server with two tools, search_knowledge and get_article. The
// contract client it is given carries the caller's credential (the static API
// token today) and sends it on every call. API errors become tool errors with
// the envelope's message and hint, and a rate-limited call says how many
// seconds to wait, from the Retry-After the agent cannot see. Error answers
// are read by contract.Answer, which hands only a 200 to the generated parser.
// With the Local option (goldie mcp --dir) a server that cannot be reached is
// named the embedded server, with no address hint.
// Text from the API is untrusted, so it is cleaned and only ever returned as
// tool output, never as tool metadata.
//
// The integration tests are in internal/app, because this package may not
// import the API or the composition root, not even in tests.
package mcp
