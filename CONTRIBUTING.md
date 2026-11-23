# Contributing to goldie

Thanks for helping. Agents follow the same rules; the agent-specific mechanics are in
[CLAUDE.md](CLAUDE.md) and [docs/agents/](docs/agents/).

## Workflow

1. Pick an issue (or open one). Larger changes start with a discussion on the issue.
2. Branch `<type>/issue-<n>-<slug>` and open a **draft PR** early, with `Closes #<n>`.
3. Build with Go 1.27. Run `./run check`; it is what CI runs and must pass with zero warnings.
4. Work through [docs/testing-checklist.md](docs/testing-checklist.md), then mark the PR ready.
   `./run gate origin/main HEAD` tells you whether the PR needs the maintainer's approval (agent
   guardrails, the decision record, CI and release, the gate tooling, new dependencies, a breaking
   contract change; a change to `run` only when it weakens a check);
   everything else merges after review and green CI.
5. The PR title is a Conventional Commit (`feat:`, `fix:`, `docs:`, ...). PRs are squash-merged, so the
   title becomes the commit message and drives release notes.

## UI evidence

A PR that changes the UI (`web/**`, `internal/ui/**`, `docs/design/**`) carries screenshot evidence
for the UX review. `./run ux` produces it: it needs Node (the version in `web/.nvmrc`) and downloads
Playwright's Chromium on first use. It builds the UI and the binary, serves `examples/knowledge` on
loopback, and runs the Playwright specs in `web/e2e/`: screenshots at 375, 768 and 1280 px in light
and dark (`shell-<width>-<theme>[-drawer].png`), axe with zero violations, no horizontal scroll and
targets of at least 44 px. The output lands in `ux/`. CI runs the same in the `ux` job and uploads
`ux/` as the `ux` artifact. The screenshots stay artifacts: never commit them to
`docs/design/screenshots/`, which holds the design references.

Use the vocabulary in [CONTEXT.md](CONTEXT.md). Record a decision as an ADR in [docs/adr/](docs/adr/)
only when it is hard to reverse, surprising and a real trade-off.

Report vulnerabilities privately, as described in [SECURITY.md](SECURITY.md).

## Versioning policy

One version covers the binary, the image and the Helm chart; release-please manages it
([ADR 0011](docs/adr/0011-versioning-policy.md)).

- **Additive-only from the first release:** the API (`/api/v1`) and the MCP contract (tool and prompt
  shapes). New fields, endpoints and tools may appear; existing ones do not change meaning or go away.
  A breaking change needs a new API version.
- **May change in `0.x` minor releases until `1.0`:** CLI flags, config variables and Helm values.
  Breaking changes are called out in the release notes. At `1.0` they freeze too.
