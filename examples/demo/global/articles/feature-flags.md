---
name: Feature flags
keywords: [feature flags, feature flag, rollout, toggles, dark launch]
---
Ship unfinished work behind a flag, so a release is never a launch.

- Name a flag for the behaviour it enables, such as `new-checkout`, and default it to off.
- Roll out in steps: staff, 5 percent, 50 percent, everyone.
- Remove the flag and its dead branch within two weeks of the full rollout.
- Keep the number of flags a service reads at startup below 20.
