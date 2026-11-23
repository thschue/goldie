# Testing checklist

Work through this before a PR leaves draft, and tick only what applies. `./run check` must already
exit 0 with zero warnings. Reviewers use the same list.

## Always

- [ ] Tests exercise external behaviour at the highest seam named in the issue: HTTP request and
      response, command output and files written, MCP messages, or a rendered page. They do not assert
      on internals. The exception is pure table tests for search scoring and Resolution rules.
- [ ] Each new test fails without the change (red first) and passes with it: `./run red-check`
      fails the tests on `origin/main`, or the PR says why a test pins existing behaviour.
- [ ] Existing test expectations were not changed to make the suite pass (in the loops,
      `scripts/test-lock` enforces this).
- [ ] No test depends on the network, the wall clock without a fake, or another test's state.
- [ ] No new dependency or pinned tool unless the issue asks for one, each with a
      [dependency brief](agents/README.md#dependency-brief); `go.sum` and lockfiles are
      untouched by hand. `./run vuln` is clean.
- [ ] `./run gate origin/main HEAD` was run, and its reasons are in the PR body.
- [ ] Docs changed in the same PR where behaviour, config, vocabulary or a decision changed.

## Hostile input

- [ ] Every interpolation point was tried with hostile input: `..`, absolute paths, symlinks, NUL and
      newline bytes, oversized values, markers and markup (`<!--`, `<script>`, backticks) and
      Markdown or YAML that tries to break out.
- [ ] Source content is treated as untrusted: invalid content is rejected while the last good
      Snapshot keeps serving; archives with absolute paths, `..` or symlinks are rejected.
- [ ] Markdown is rendered without raw HTML and sanitised.
- [ ] A new parser of hostile input (Source URL, archive path, Markdown, YAML) has a Go fuzz target.
- [ ] Query text stays out of metric labels.

## API and contract

- [ ] Responses validate against `openapi.yaml`, and the route-coverage test shows spec and router
      agree in both directions.
- [ ] Errors use the `{"error":{code,message,details}}` envelope, with a next-step hint in `details`
      and no addresses, paths or stack traces.
- [ ] The change is additive for `/api/v1` and the MCP contract (see CONTRIBUTING.md); `./run gate`
      reports no breaking contract change.
- [ ] `./run regen` produces no diff.

## Auth and security

- [ ] Each endpoint was tried unauthenticated, as `reader` and as `operator`.
- [ ] Session and login hardening holds: forged Origin, missing CSRF, replayed state, expiry, use
      after logout.
- [ ] No secret, token or GitHub credential appears in logs, errors, responses or test fixtures.
- [ ] `/security-review` ran on the diff and its findings are fixed or explained (for loop PRs,
      the `security-reviewer` and `reviewer` pass replaces it).

## Platform Contract

- [ ] A change to `server` or `mcp` behaviour keeps every `PC-xx` clause in
      [docs/platform-contract.md](platform-contract.md); clause tests run against the real server.

## CLI and local mode

- [ ] CLI tests run the root command in-process with args, env and a temporary project dir.
- [ ] Local (embedded) mode gives the same results as remote mode for the same content.
- [ ] Files outside the Goldie namespace and outside managed blocks are left byte-identical.

## UI

- [ ] The UI work follows an approved design handoff (§13 of `docs/design/ui-handoff.md`).
- [ ] **Matches the handoff**: the screen implements the layout, components, states (§5), copy (§8)
      and breakpoints (§7) of `docs/design/ui-handoff.md`; component tests cover every state marked
      for that component; Playwright covers the screen at 375 / 768 / 1280 in light and dark, with
      an axe run that has zero violations.
- [ ] `./run ux` passes, and the PR's `ux` CI artifact holds the screenshots for the UX review
      (never committed to `docs/design/screenshots/`).
- [ ] Every changed flow works keyboard-only with visible focus; no state is shown by colour alone.
