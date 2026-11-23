// Package mcp is the MCP component. It reaches the API only through the
// contract client, never through the engine or api packages.
//
// New returns a server with four tools: search_knowledge and get_article,
// which take an optional project (an ASCII ProjectId, lower-cased and checked
// before any request), list_projects, and current_context, which reports the
// default Project set with WithProject, the problem set with
// WithProjectProblem, or that there is none. An omitted or empty project
// takes the default, or fails with the problem before any request. The
// contract client it is given carries the caller's credential (the static API
// token today) and sends it on every call. API errors become tool errors with
// the envelope's message and hint, and a rate-limited call says how many
// seconds to wait, from the Retry-After the agent cannot see. Error answers
// are read by contract.Answer, which hands only a 200 to the generated parser.
// With the Local option (goldie mcp --dir) a server that cannot be reached is
// named the embedded server, with no address hint.
// Text from the API is untrusted, so it is cleaned and only ever returned as
// tool output, never as tool metadata. get_article of a deprecated article
// starts with the notice goldie lookup prints on stderr, naming the cleaned
// replacement. MCP never asks for drafts.
//
// The integration tests are in internal/app, because this package may not
// import the API or the composition root, not even in tests.
package mcp
