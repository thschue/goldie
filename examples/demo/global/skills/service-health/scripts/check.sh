#!/bin/sh
# Usage: sh scripts/check.sh <base-url>
# Prints healthy and exits 0 when <base-url>/healthz answers 200.
set -eu
[ $# -eq 1 ] || { echo "usage: check.sh <base-url>" >&2; exit 2; }
code=$(curl -s -o /dev/null -m 5 -w '%{http_code}' "$1/healthz" || true)
if [ "$code" = "200" ]; then echo healthy; else echo unhealthy; exit 1; fi
