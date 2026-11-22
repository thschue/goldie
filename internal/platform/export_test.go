package platform

import "time"

// appReadTimeout is the test hook for the public listener's ReadTimeout
// (issue #170), next to readHeaderTimeout, idleTimeout and adminIOTimeout.
// This declaration is a stub so the acceptance tests build. The implementer
// moves it into server.go as `appReadTimeout = <production value>` in the
// same var block, wires it to the public http.Server's ReadTimeout, and
// deletes this stub (the test author approves that removal).
var appReadTimeout = time.Duration(0)
