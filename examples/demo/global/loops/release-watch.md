---
name: release-watch
description: After a release, watch the service until it is healthy and report whether the release can stay.
keywords: [release, watch, rollout, health]
dynamic: true
agents: [release-reviewer]
skills: [service-health]
stop: "The service is healthy for ten minutes, or the release is rolled back."
---
Run one tick:

1. Run the `service-health` skill against the service.
2. If it is unhealthy twice in a row, report the failure and recommend a rollback.
3. If it is healthy, raise the feature flag one rollout step.
4. On the first tick, ask `agent:release-reviewer` to review the release.
