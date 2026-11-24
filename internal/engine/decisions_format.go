package engine

import (
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// A decision file without goldie frontmatter is read as Nygard (adr-tools) or
// MADR 2.x: parseDecision fills the title, status, date and supersession from
// the body, then runs its usual checks. Nothing here validates; a value it
// cannot read is left out, and the checks name the missing key.

var (
	headingNumber  = regexp.MustCompile(`^\d+\.\s+`)
	dateLine       = regexp.MustCompile(`(?i)^(?:[*-]\s+)?date:\s*(.*)$`)
	statusBullet   = regexp.MustCompile(`(?i)^[*-]\s+status:\s*(.*)$`)
	supersededLink = regexp.MustCompile(`(?i)^superseded by \[[^\]]*\]\(([^)]*)\)`)
)

// bodyLines splits a body into lines outside fenced code blocks, so a fenced
// "# comment" is not a heading.
func bodyLines(body []byte) []string {
	var out []string
	fenced := false
	for _, l := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			out = append(out, l)
		}
	}
	return out
}

// firstH1 is the first H1 of a body without its leading number ("1. Title").
func firstH1(lines []string) string {
	for _, l := range lines {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			return headingNumber.ReplaceAllString(strings.TrimSpace(t), "")
		}
	}
	return ""
}

// bodyDecisionFrontmatter builds the frontmatter of a decision file that has
// none: title, status, superseded_by and date as the body gives them. The
// values are marshalled, so no file text becomes YAML structure.
func bodyDecisionFrontmatter(body []byte) []byte {
	lines := bodyLines(body)
	// head is what comes before the first H2: where the Date line and the MADR
	// bullets live.
	head := lines
	for i, l := range lines {
		if strings.HasPrefix(l, "## ") {
			head = lines[:i]
			break
		}
	}
	status, date := "", ""
	for _, l := range head {
		l = strings.TrimSpace(l)
		if m := dateLine.FindStringSubmatch(l); m != nil && date == "" {
			date = strings.TrimSpace(m[1])
		} else if m := statusBullet.FindStringSubmatch(l); m != nil && status == "" {
			status = strings.TrimSpace(m[1])
		}
	}
	if status == "" {
		status = nygardStatus(lines)
	}
	meta := map[string]string{"title": firstH1(lines)}
	if m := supersededLink.FindStringSubmatch(status); m != nil {
		// Only a bare <stem>.md names a decision; anything else is left out
		// and fails as a superseded status without superseded_by.
		if stem, ok := strings.CutSuffix(m[1], ".md"); ok && stem != "" && !strings.Contains(stem, "/") {
			meta["superseded_by"] = KindDecision + ":" + stem
		}
	}
	if f := strings.Fields(status); len(f) > 0 {
		meta["status"] = strings.ToLower(f[0])
	}
	out, _ := yaml.Marshal(meta)
	// A day is written plain so it resolves as a date; anything else is
	// left out and fails the date check.
	if _, err := time.Parse(time.DateOnly, date); err == nil {
		out = append(out, "date: "+date+"\n"...)
	}
	return out
}

// nygardStatus is the first non-empty line of the "## Status" section.
func nygardStatus(lines []string) string {
	in := false
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "## "):
			if in {
				return ""
			}
			in = strings.EqualFold(strings.TrimSpace(l[3:]), "status")
		case in && strings.TrimSpace(l) != "":
			return strings.TrimSpace(l)
		}
	}
	return ""
}
