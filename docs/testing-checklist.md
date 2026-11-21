# Testing checklist

Work through this before a PR leaves draft, and tick only what applies. `./run check` must already
exit 0 with zero warnings. Reviewers use the same list.

## Always

- [ ] Tests exercise external behaviour at the highest seam named in the issue: HTTP request and
      response, command output and files written, MCP messages, or a rendered page. They do not assert
      on internals. The exception is pure table tests for search scoring and Resolution rules.
- [ ] Each new test fails without the change (red first) and passes with it.
- [ ] Existing test expectations were not changed to make the suite pass.
- [ ] No test depends on the network, the wall clock without a fake, or another test's state.
- [ ] No new dependency unless the issue asks for one; `go.sum` and lockfiles are untouched by hand.
- [ ] Docs changed in the same PR where behaviour, config, vocabulary or a decision changed.

## Hostile input

- [ ] Every interpolation point was tried with hostile input: `..`, absolute paths, symlinks, NUL and
      newline bytes, oversized values, markers and markup (`<!--`, `<script>`, backticks) and
      Markdown or YAML that tries to break out.
- [ ] Source content is treated as untrusted: invalid content is rejected while the last good
      Snapshot keeps serving; archives with absolute paths, `..` or symlinks are rejected.
- [ ] Markdown is rendered without raw HTML and sanitised.
- [ ] Query text stays out of metric labels (and out of spans unless `GOLDIE_TELEMETRY_QUERY_TEXT`).

## API and contract

- [ ] Responses validate against `openapi.yaml`, and the route-coverage test shows spec and router
      agree in both directions.
- [ ] Errors use the `{"error":{code,message,details}}` envelope, with a next-step hint in `details`
      and no addresses, paths or stack traces.
- [ ] The change is additive for `/api/v1` and the MCP contract (see CONTRIBUTING.md).
- [ ] `./run regen` produces no diff.

## Auth and security

- [ ] Each endpoint was tried unauthenticated, as `reader` and as `operator`.
- [ ] Session and login hardening holds: forged Origin, missing CSRF, replayed state, expiry, use
      after logout.
- [ ] No secret, token or GitHub credential appears in logs, errors, responses or test fixtures.
- [ ] `/security-review` ran on the diff and its findings are fixed or explained.

## Platform Contract

- [ ] A change to `server` or `mcp` behaviour keeps every `PC-xx` clause in
      [docs/platform-contract.md](platform-contract.md); clause tests run against the real server.

## CLI and local mode

- [ ] CLI tests run the root command in-process with args, env and a temporary project dir.
- [ ] Local (embedded) mode gives the same results as remote mode for the same content.
- [ ] Files outside the Goldie namespace and outside managed blocks are left byte-identical.

## UI

- [ ] The UI work follows an approved design handoff; states from the handoff have component tests.
- [ ] Light and dark themes, keyboard use and WCAG 2.1 AA spot checks pass.
