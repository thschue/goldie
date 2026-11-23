package engine

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// checkLookups fails when a lookup line in an article's body names an id that
// does not resolve in that article's own Scope view: a global article sees
// global only, a Project article its Project, then global, less the global
// Items the Project suppresses (#367), by winner's rule in the default view
// (resolvePublished). A draft is left out of Resolution (#353), so a lookup of
// a draft is dangling too. A placeholder such as <id>
// or … is skipped (see isPlaceholder). The error names the article, and the
// project.yaml when that is why the id is hidden, and quotes the id, bounded
// and escaped, never raw. all is the Articles to check, so a Source that adds
// the local Scope checks only the local Items; a local Item sees local, then
// global (ADR 0021).
func (s *Snapshot) checkLookups(all []*Article) error {
	for _, a := range all {
		view := viewOf(a)
		for _, id := range lookupIDs(a.Content) {
			if isPlaceholder(id) {
				continue
			}
			// An id that is not a valid name is dangling.
			if key, err := parseRef(KindArticle + ":" + id); err == nil {
				if file, hidden := s.suppressingFile(view, key); hidden {
					return fmt.Errorf("%s: goldie lookup %s names an Item %s suppresses", articleFile(a), quoteID(id), file)
				}
				if s.resolvePublished(view, key) != nil {
					continue
				}
			}
			if len(id) > maxRefBytes {
				return fmt.Errorf("%s: a goldie lookup line has an id longer than %d bytes", articleFile(a), maxRefBytes)
			}
			return fmt.Errorf("%s: goldie lookup %s does not resolve to an Item this article can see", articleFile(a), quoteID(id))
		}
	}
	return nil
}

// isPlaceholder reports whether a lookup id is a placeholder, as in the README's
// `goldie lookup <id>`: an id in angle brackets, or an ellipsis (… or ...). An
// id with a hidden, control or non-printing rune, or longer than a reference
// may be, is never a placeholder; it stays dangling and fails the load.
func isPlaceholder(id string) bool {
	if len(id) > maxRefBytes {
		return false
	}
	for _, r := range id {
		if r == utf8.RuneError || !unicode.IsPrint(r) || HiddenRune(r) {
			return false
		}
	}
	return id == "…" || id == "..." || (len(id) > 2 && id[0] == '<' && id[len(id)-1] == '>')
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

// lookupIDs returns the id of every lookup line in a Markdown body: a line of a
// fenced code block, or an inline code span within one line, whose whole
// content is `goldie lookup <id>`. It follows CommonMark where that decides
// whether a backtick is code: a fence is indented at most 3 columns from its
// container, a list item is a container, and an indented code block and a
// <pre> HTML block are not scanned. It is one linear pass over the body.
func lookupIDs(body string) []string {
	var ids []string
	add := func(content string) {
		if id, ok := lookupLine(content); ok {
			ids = append(ids, id)
		}
	}
	var (
		items      []int // content column of each open list item
		fenceChar  byte
		fenceLen   int
		fenceDepth int // len(items) when the fence opened
		inPre      bool
		inPara     bool
	)
	for line := range strings.SplitSeq(body, "\n") {
		if inPre { // an unclosed <pre> runs to the end of the body
			inPre = !containsFold(line, "</pre>")
			continue
		}
		col, rest := leadingColumns(line)
		blank := isBlank(rest)
		if !blank { // a line indented less than an item's content ends the item
			for len(items) > 0 && col < items[len(items)-1] {
				items = items[:len(items)-1]
			}
			if fenceChar != 0 && len(items) < fenceDepth {
				fenceChar = 0
			}
		}
		base := 0
		if len(items) > 0 {
			base = items[len(items)-1]
		}
		if fenceChar != 0 { // an unterminated fence runs to the end of the body
			if n := runLen(rest, fenceChar); col-base <= 3 && n >= fenceLen && isBlank(rest[n:]) {
				fenceChar = 0
				continue
			}
			add(line)
			continue
		}
		if blank {
			inPara = false
			continue
		}
		for col-base <= 3 { // list markers, which may nest on one line
			m, ok := listMarker(rest)
			if !ok {
				break
			}
			sp, after := leadingColumns(rest[m:])
			content, extra := col+m+sp, 0
			if isBlank(after) || sp > 4 {
				content, extra = col+m+1, max(sp-1, 0)
			}
			items = append(items, content)
			base, col, rest = content, content+extra, after
			inPara = false
		}
		if isBlank(rest) {
			continue
		}
		if col-base >= 4 {
			if !inPara { // an indented code block
				continue
			}
		} else if isPreStart(rest) {
			inPre = !containsFold(rest, "</pre>")
			inPara = false
			continue
		} else if c := rest[0]; c == '`' || c == '~' {
			n := runLen(rest, c)
			if n >= 3 && (c == '~' || !strings.Contains(rest[n:], "`")) {
				fenceChar, fenceLen, fenceDepth = c, n, len(items)
				inPara = false
				continue
			}
		}
		for _, span := range codeSpans(rest) {
			add(span)
		}
		inPara = true
	}
	return ids
}

// leadingColumns returns the width of s's leading spaces and tabs in columns
// (a tab advances to the next multiple of 4) and the rest of s.
func leadingColumns(s string) (int, string) {
	col := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ':
			col++
		case '\t':
			col += 4 - col%4
		default:
			return col, s[i:]
		}
	}
	return col, ""
}

func isBlank(s string) bool { return strings.Trim(s, " \t\r") == "" }

// listMarker returns the byte length of a list marker at the start of s: one
// of - + *, or up to nine digits then . or ), followed by a space, a tab or the
// end of the line.
func listMarker(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 1
	switch c := s[0]; {
	case c == '-' || c == '+' || c == '*':
	case c >= '0' && c <= '9':
		for n < len(s) && s[n] >= '0' && s[n] <= '9' {
			n++
		}
		if n > 9 || n >= len(s) || (s[n] != '.' && s[n] != ')') {
			return 0, false
		}
		n++
	default:
		return 0, false
	}
	if n < len(s) && s[n] != ' ' && s[n] != '\t' && s[n] != '\r' {
		return 0, false
	}
	return n, true
}

// isPreStart reports whether s opens a <pre> HTML block.
func isPreStart(s string) bool {
	if len(s) < 4 || !strings.EqualFold(s[:4], "<pre") {
		return false
	}
	return len(s) == 4 || s[4] == ' ' || s[4] == '\t' || s[4] == '>' || s[4] == '\r'
}

// containsFold reports whether s holds sub, ignoring ASCII case, without
// allocating. sub is lower case.
func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
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
// The next run of each length is precomputed, so the scan stays linear. A line
// with no backtick returns at once.
func codeSpans(line string) []string {
	if strings.IndexByte(line, '`') < 0 {
		return nil
	}
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
