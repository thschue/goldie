# 19. The SPA's build output is not committed; the binary embeds it when built, otherwise a placeholder

Status: proposed

## Context

ADR 0010 embeds the Vue SPA in the binary. Vite's output has hashed names and needs Node. `go build`, `go install` and `./run check` must keep working for Go-only contributors, and committed build output can't be checked for drift without Node in `check`. Rejected: committing `dist` (drift is unverifiable and diffs are noisy) and a build tag (two binaries, two test matrices).

## Decision

`./run ui` builds into `internal/ui/dist/app/`, which is gitignored. `internal/ui` embeds `all:dist`, including a committed placeholder page, and serves the real build when present, otherwise the placeholder. The server logs a warning at startup when it serves the placeholder. Release builds and the `ux` CI job run `./run ui` first, and a release fails if it would ship the placeholder.

## Consequences

Git holds no build output, and the Go toolchain alone still builds and tests the binary. `go install` and any binary built without `./run ui` serve a placeholder UI, so packaging must run `./run ui`. Reversing this changes the release pipeline and the contributor workflow.
