package app

import (
	"os"
	"strings"
)

// activityReplicaName is this replica's own name, the replica of every event
// it records; the public handler passes it to api.NewActivity. A variable so
// tests can name a replica; it is read when the public handler is built.
var activityReplicaName = func() string {
	h, _ := os.Hostname()
	return replicaNameFromHostname(h)
}

// replicaNameFromHostname is a replica's own name: the hostname's first
// label, lower-cased (a StatefulSet pod name), or "goldie" when that is not
// a DNS label of 1 to 63 of [a-z0-9-] that neither starts nor ends with a
// hyphen. It never carries a domain or an address.
func replicaNameFromHostname(hostname string) string {
	label, _, _ := strings.Cut(strings.ToLower(hostname), ".")
	if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return "goldie"
	}
	for _, c := range []byte(label) {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return "goldie"
		}
	}
	return label
}
