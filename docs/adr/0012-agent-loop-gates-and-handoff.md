# 12. Agent loops: mechanical gates, a PR-centric handoff and review lenses

Status: accepted

## Context

The three agent loops (issue-worker, review, pm-audit) needed a human far more often than security
or product direction calls for. The protected-path list held every PR that touched `go.mod`,
`go.sum`, `run` or a test fixture, so nearly every feature PR waited for a manual merge (#50 waited
eight hours after a clean review). Rework travelled through issue labels, so a PR without an issue
was orphaned after a hand-back (#44). The reviewer held PRs whose CI was still running, although the
`main` ruleset already makes auto-merge wait for the required checks. Self-fix covered every nit,
and each fixed head got a full fresh review. The rules were restated in four places, and nothing but
prose kept an agent from merging with `--admin` or pushing to `main`.

## Decision

- **Human gates exist for security and product direction only**, and a script decides them:
  `scripts/agent-gate`, which the review loop always runs from `main`. It gates:
  - agent guardrails and instruction files anywhere in the tree;
  - the decision record;
  - CI, release and architecture enforcement;
  - deployment;
  - the check and loop tooling (`run`, `scripts/`), because the PR's own `run` defines what green
    CI means;
  - a new direct dependency or pinned tool, after the loop has vetted it with a dependency brief;
  - a `go.mod` `replace` or similar directive;
  - a breaking change to the API contract.

  Everything else that a fresh reviewer finds clean auto-merges, and GitHub waits for CI.
- **Implementer and reviewer stay separate loops**, for throughput (two sessions pipeline) and for
  independent verification. They hand off through PR state:
  - the reviewer reviews every non-draft PR without a review marker for its head;
  - the worker reworks every own PR labelled `changes-requested`.

  Four flow labels remain: `in-progress`, `in-review`, `changes-requested` and `needs-maintainer`.
- **Review is severity-proportional and split into lenses.**
  - Lenses: security always, architecture for Go code and the contract, UX for the UI.
  - Blocking findings are fixed, in at most one self-fix round, which a fresh reviewer checks with
    a delta review.
  - Nits never cause a round.
- **Guardrails are enforced by the harness**, with prose as the explanation:
  - a `PreToolUse` hook (`scripts/loop-guard`);
  - a permission allowlist in `.claude/settings.json`;
  - agent definitions in `.claude/agents/`, kept byte-identical to the example agent Items.
- `docs/agents/` is the single source of the loop rules. The commands and the example loop Items
  only point to it.

## Consequences

Most PRs merge without a human. The maintainer sees a short inbox of real decisions. The cost:
- **More rides on CI and the reviewer.** A weakened check would let bad code through. The gate
  therefore covers everything that defines the checks (`run`, `scripts/`, the linter configuration,
  CI). The loop and CI run `main`'s copy of the gate, so a PR can't loosen the gate that judges it.
  The `main` ruleset pins the required checks to the GitHub Actions app, so a written commit status
  can't stand in for them.
- **The hook matches command text.** It stops mistakes, not a determined attacker. Branch rules and
  the review remain the real controls.
- **This ADR changes earlier practice.** ADR 0001 calls the depguard rules a protected path; they
  still are, under the gate's "architecture enforcement" reason.
