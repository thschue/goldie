package app

import (
	"os"
	"strings"

	"github.com/thschue/goldie/internal/platform"
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
// a DNS label (replicaLabel). It never carries a domain or an address.
func replicaNameFromHostname(hostname string) string {
	label, _, _ := strings.Cut(hostname, ".")
	if name, ok := replicaLabel(label); ok {
		return name
	}
	return "goldie"
}

// replicaLabel is the one rule a replica's name passes, whether it comes from
// this host's name or a peer's PTR record: label must be a DNS label as
// written (platform.IsDNSLabel), and the name is it lower-cased. The check
// comes first, because strings.ToLower maps some non-ASCII runes, such as the
// Kelvin sign U+212A, to ASCII letters.
func replicaLabel(label string) (string, bool) {
	if !platform.IsDNSLabel(label) {
		return "", false
	}
	return strings.ToLower(label), true
}
