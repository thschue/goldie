---
name: Retries and timeouts
keywords: [retries, retry, timeouts, backoff, idempotency]
---
A call to another service can fail for a moment. Retry it, but carefully.

- Retry only idempotent calls, or calls that carry an idempotency key.
- Use exponential backoff with jitter: 100 ms, 200 ms, 400 ms, at most three retries.
- Set a timeout on every call; a call without one waits forever.
- Stop retrying on a 4xx response: the request is wrong, not the service.
