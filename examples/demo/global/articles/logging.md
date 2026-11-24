---
name: Logging in Harbor services
keywords: [logging, logs, structured logs, log levels, request id]
---
Services write one JSON object per line to standard output. The platform collects it.

- Use the levels `debug`, `info`, `warn` and `error`; production runs at `info`.
- Include the `request_id` from the incoming request in every record.
- Never log tokens, passwords or personal data.
- Log an error once, where it is handled, not at every layer it passes through.
