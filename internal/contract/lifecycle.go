package contract

import "strings"

// Deprecation classifies an Article for the clients that warn about it: ok
// when its status is deprecated, and ref the reference that replaces it,
// cleaned with CleanLine and trimmed, or "" when there is none or nothing is
// left of it. Each client words its own notice from it; the status is never
// echoed.
func Deprecation(a Article) (ref string, ok bool) {
	if a.Status == nil || *a.Status != ItemStatusDeprecated {
		return "", false
	}
	if a.ReplacedBy != nil {
		ref = strings.TrimSpace(CleanLine(*a.ReplacedBy))
	}
	return ref, true
}
