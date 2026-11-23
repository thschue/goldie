package contract

import "strings"

// statusDeprecated is the Article status of an Item that is still served but
// should no longer be used (CONTEXT: Status).
const statusDeprecated = "deprecated"

// DeprecationNotice is the one line a client shows before an Article whose
// status is deprecated, without a newline, or "" for any other status. The CLI
// prints it on stderr and MCP's get_article puts it first, so both say the
// same. The replacement is server text, cleaned with CleanLine; the status is
// never echoed.
func DeprecationNotice(a Article) string {
	if a.Status == nil || *a.Status != statusDeprecated {
		return ""
	}
	if a.ReplacedBy != nil {
		if ref := strings.TrimSpace(CleanLine(*a.ReplacedBy)); ref != "" {
			return "Warning: this article is deprecated. Use " + ref + " instead."
		}
	}
	return "Warning: this article is deprecated."
}
