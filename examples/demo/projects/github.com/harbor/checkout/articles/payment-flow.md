---
name: Checkout payment flow
keywords: [checkout, payment, orders, flow]
---
Only the checkout service has this flow.

1. Reserve the stock for the order.
2. Charge the payment provider with the order's idempotency key.
3. Confirm the order, or release the stock when the charge fails.
