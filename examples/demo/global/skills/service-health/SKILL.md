---
name: service-health
description: Check whether a Harbor service answers on its health endpoint, with a script that reports the status.
keywords: [health, health check, service, uptime]
---
Run `sh scripts/check.sh <url>` with a service's base URL. It prints `healthy` and exits 0 when
`<url>/healthz` answers 200, and prints `unhealthy` and exits 1 otherwise.

Use it before and after a release to see whether the service came up.
