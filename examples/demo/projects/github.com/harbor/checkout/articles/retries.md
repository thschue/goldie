---
name: Retries and timeouts in checkout
keywords: [retries, retry, timeouts, backoff, payments]
---
Checkout overrides the platform rule for calls to the payment provider.

- Never retry a charge without the order's idempotency key.
- Retry at most twice, 200 ms then 400 ms, with jitter.
- Give every payment call a 3 s timeout; the shopper is waiting.
- Stop on a 4xx response and show the shopper the provider's reason.
