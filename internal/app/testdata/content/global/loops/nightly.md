---
name: nightly
description: Keep the nightly build green
interval: 30m
agents: [reviewer]
stop: when CI is green
---
Check the nightly build and fix what broke.
