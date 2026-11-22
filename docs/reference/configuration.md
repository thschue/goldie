<!-- Generated from the settings registry in internal/platform/settings.go by `./run regen`. Do not edit. -->

# Goldie configuration reference

Configuration is environment only (PC-04). Every variable also accepts a `NAME_FILE` form
that reads the value from a file; setting both is an error (PC-05).

| Variable | Default | Meaning |
|---|---|---|
| `GOLDIE_ADDR` | `:8080` | Public (app) listener. |
| `GOLDIE_ADMIN_ADDR` | `:8081` | Admin listener: /livez, /readyz, /version. Must differ from GOLDIE_ADDR. |
| `GOLDIE_DRAIN_SECONDS` | `25` | Time in seconds between failing readiness on SIGTERM and shutting the listeners down. 0 to 3600. |
| `GOLDIE_LOG_LEVEL` | `info` | Log level: debug, info, warn or error. |
| `GOLDIE_SWAGGER_UI_ENABLED` | `true` | Serve Swagger UI at /swagger-ui on the public listener, from assets embedded in the binary. true or false; false answers 404. |
