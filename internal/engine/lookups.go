package engine

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// checkLookups fails when a lookup line in an article's body names an id that
// does not resolve in that article's own Scope view: a global article sees
// global only, a Project article its Project, then global. A draft is left out
// of Resolution (#353), so a lookup of a draft is dangling too. The error
// names the article and quotes the id, bounded and escaped, never raw.
func (s *Snapshot) checkLookups() error {
	for _, a := range s.allArticles() {
		for _, id := range lookupIDs(a.Content) {
			if s.lookupResolves(a, id) {
				continue
			}
			if len(id) > maxRefBytes {
				return fmt.Errorf("%s: a goldie lookup line has an id longer than %d bytes", articleFile(a), maxRefBytes)
			}
			return fmt.Errorf("%s: goldie lookup %s does not resolve to an Item this article can see", articleFile(a), quoteID(id))
		}
	}
	return nil
}

// quoteID renders an id for an error message: escaped, so a control, format or
// bidi rune never appears raw, and cut to maxQuotedRefRunes runes. It does not
// redact, because an id such as team/policy is a path-shaped name, not a path.
func quoteID(id string) string {
	if utf8.RuneCountInString(id) > maxQuotedRefRunes {
		id = string([]rune(id)[:maxQuotedRefRunes]) + "…"
	}
	return strconv.QuoteToASCII(id)
}

// lookupResolves reports whether id names a published Item in a's view. An id
// that is not a valid name is dangling.
func (s *Snapshot) lookupResolves(a *Article, id string) bool {
	key, err := parseRef(KindArticle + ":" + id)
	if err != nil {
		return false
	}
	view := a.Scope
	if view == ScopeGlobal {
		view = ""
	}
	return s.resolvePublished(view, key) != nil
}

// lookupIDs returns the id of every lookup line in a Markdown body: a line of a
// fenced code block, or an inline code span within one line, whose whole
// content is `goldie lookup <id>`. It is one linear pass over the body.
func lookupIDs(body string) []string {
	var ids []string
	add := func(content string) {
		if id, ok := lookupLine(content); ok {
			ids = append(ids, id)
		}
	}
	var fenceChar byte
	fenceLen := 0
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if fenceChar != 0 { // an unterminated fence runs to the end of the body
			if n := runLen(trimmed, fenceChar); n >= fenceLen && strings.Trim(trimmed[n:], " \t\r") == "" {
				fenceChar = 0
				continue
			}
			add(line)
			continue
		}
		if trimmed != "" && (trimmed[0] == '`' || trimmed[0] == '~') {
			n := runLen(trimmed, trimmed[0])
			if n >= 3 && (trimmed[0] == '~' || !strings.Contains(trimmed[n:], "`")) {
				fenceChar, fenceLen = trimmed[0], n
				continue
			}
		}
		for _, span := range codeSpans(line) {
			add(span)
		}
	}
	return ids
}

// runLen counts the leading c characters of s.
func runLen(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

// lookupLine parses `goldie lookup <id>`: exactly three words separated by
// spaces or tabs. Anything else, including a --project flag, is not a lookup
// line.
func lookupLine(content string) (string, bool) {
	f := strings.FieldsFunc(content, func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' })
	if len(f) != 3 || f[0] != "goldie" || f[1] != "lookup" {
		return "", false
	}
	return f[2], true
}

// codeSpans returns the contents of the inline code spans of one line: a run of
// n backticks closed by the next run of exactly n. An unclosed run is literal.
// The next run of each length is precomputed, so the scan stays linear.
func codeSpans(line string) []string {
	type run struct{ start, n int }
	var runs []run
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		n := runLen(line[i:], '`')
		runs = append(runs, run{i, n})
		i += n
	}
	next := make([]int, len(runs))
	last := map[int]int{}
	for i := len(runs) - 1; i >= 0; i-- {
		next[i] = -1
		if j, ok := last[runs[i].n]; ok {
			next[i] = j
		}
		last[runs[i].n] = i
	}
	var spans []string
	for i := 0; i < len(runs); i++ {
		if j := next[i]; j >= 0 {
			spans = append(spans, line[runs[i].start+runs[i].n:runs[j].start])
			i = j
		}
	}
	return spans
}
