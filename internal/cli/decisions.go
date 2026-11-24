package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/thschue/goldie/internal/contract"
)

// decisions runs goldie decisions list|show. The server resolves the view and
// the status filter (accepted by default); the CLI only prints. Pattern: loops.
func (c *commander) decisions(ctx context.Context, verb, name string) error {
	proj, err := c.project()
	if err != nil {
		return err
	}
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	if verb == "show" {
		raw, err := cl.GetDecision(ctx, name, &contract.GetDecisionParams{Project: projectParam(proj)})
		d, err := contract.Answer(raw, err, contract.ParseGetDecisionResponse,
			func(r *contract.GetDecisionResponse) *contract.Decision { return r.JSON200 })
		if err != nil {
			return wordError(contract.RewordMiss(err, contract.DecisionMissHint, "List the decisions with: goldie decisions list, then use a name from its results."), c.local)
		}
		if c.s.json {
			return c.printJSON(d)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n%s\nscope: %s\nstatus: %s\ndate: %s\n",
			contract.CleanLine(d.Name), contract.CleanLine(d.Title), contract.CleanLine(d.Scope),
			contract.CleanLine(string(d.Status)), contract.CleanLine(d.Date))
		if d.Content != nil {
			fmt.Fprintf(&b, "\n%s\n", strings.TrimRight(contract.CleanText(*d.Content), "\n"))
		}
		writeHint(&b, d.Hint)
		_, err = fmt.Fprint(c.out, b.String())
		return err
	}
	raw, err := cl.ListDecisions(ctx, &contract.ListDecisionsParams{Project: projectParam(proj)})
	l, err := contract.Answer(raw, err, contract.ParseListDecisionsResponse,
		func(r *contract.ListDecisionsResponse) *contract.DecisionList { return r.JSON200 })
	if err != nil {
		return wordError(err, c.local)
	}
	if c.s.json {
		return c.printJSON(l)
	}
	var b strings.Builder
	if len(l.Decisions) == 0 {
		b.WriteString("No decisions.\n")
	}
	for _, d := range l.Decisions {
		fmt.Fprintf(&b, "%s  %s  (%s)\n", contract.CleanLine(d.Name), contract.CleanLine(d.Title), contract.CleanLine(d.Scope))
	}
	if len(l.Decisions) > 0 {
		b.WriteString("\nRead one with: goldie decisions show <name>\n")
	}
	writeHint(&b, l.Hint)
	_, err = fmt.Fprint(c.out, b.String())
	return err
}
