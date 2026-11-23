package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/thschue/goldie/internal/contract"
)

// loops runs goldie loops list|show. The server resolves the loop and its
// closure; the CLI only prints them. Pattern: agents.
func (c *commander) loops(ctx context.Context, verb, name string) error {
	proj, err := c.project()
	if err != nil {
		return err
	}
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	if verb == "show" {
		raw, err := cl.GetLoop(ctx, name, &contract.GetLoopParams{Project: projectParam(proj)})
		l, err := contract.Answer(raw, err, contract.ParseGetLoopResponse,
			func(r *contract.GetLoopResponse) *contract.Loop { return r.JSON200 })
		if err != nil {
			return wordError(contract.RewordMiss(err, contract.LoopMissHint, "List the loops with: goldie loops list, then use a name from its results."), c.local)
		}
		if c.s.json {
			return c.printJSON(l)
		}
		var b strings.Builder
		writeLoopHead(&b, *l)
		if l.Content != nil {
			fmt.Fprintf(&b, "\n%s\n", strings.TrimRight(contract.CleanText(*l.Content), "\n"))
		}
		writeHint(&b, l.Hint)
		_, err = fmt.Fprint(c.out, b.String())
		return err
	}
	raw, err := cl.ListLoops(ctx, &contract.ListLoopsParams{Project: projectParam(proj)})
	l, err := contract.Answer(raw, err, contract.ParseListLoopsResponse,
		func(r *contract.ListLoopsResponse) *contract.LoopList { return r.JSON200 })
	if err != nil {
		return wordError(err, c.local)
	}
	if c.s.json {
		return c.printJSON(l)
	}
	var b strings.Builder
	if len(l.Loops) == 0 {
		b.WriteString("No loops.\n" + emptyNextStep)
	}
	for _, lp := range l.Loops {
		fmt.Fprintf(&b, "%s  %s\n", contract.CleanLine(lp.Name), contract.CleanLine(lp.Description))
	}
	if len(l.Loops) > 0 {
		b.WriteString("\nRead one with: goldie loops show <name>\n")
	}
	writeHint(&b, l.Hint)
	_, err = fmt.Fprint(c.out, b.String())
	return err
}

// writeLoopHead prints a loop's facts, one cleaned line each, so no server
// text can forge a line of its own.
func writeLoopHead(b *strings.Builder, l contract.Loop) {
	fmt.Fprintf(b, "%s\n%s\n", contract.CleanLine(l.Name), contract.CleanLine(l.Description))
	if l.Scope != nil {
		fmt.Fprintf(b, "scope: %s\n", contract.CleanLine(*l.Scope))
	}
	if l.Dynamic {
		b.WriteString("runs: dynamic (paces itself)\n")
	} else if l.Interval != nil {
		fmt.Fprintf(b, "runs: every %s\n", contract.CleanLine(*l.Interval))
	}
	fmt.Fprintf(b, "stop: %s\n", contract.CleanLine(l.Stop))
	writeRefs(b, "agents", l.Closure.Agents)
	writeRefs(b, "skills", l.Closure.Skills)
}

func writeRefs(b *strings.Builder, kind string, refs []contract.LoopRef) {
	if len(refs) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n", kind)
	for _, r := range refs {
		fmt.Fprintf(b, "  %s  (%s)\n", contract.CleanLine(r.Name), contract.CleanLine(r.Scope))
	}
}
