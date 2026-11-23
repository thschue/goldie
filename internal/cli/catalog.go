package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/thschue/goldie/internal/contract"
)

// catalogUsage checks the words after skills or agents (list, or show <name>)
// before any server, token or request is involved, and returns the verb and
// the name. A name that URL path cleaning would resolve into another route is
// refused as lookup refuses an article id.
func catalogUsage(kind string, pos []string) (verb, name string, err error) {
	if len(pos) == 0 {
		return "", "", &UsageError{kind, kind + " needs list or show <name>"}
	}
	switch pos[0] {
	case "list":
		if len(pos) != 1 {
			return "", "", &UsageError{kind, kind + " list takes no arguments"}
		}
		return "list", "", nil
	case "show":
		if len(pos) != 2 {
			return "", "", &UsageError{kind, kind + " show takes one <name>"}
		}
		if contract.CheckArticleID(pos[1]) != nil || strings.Contains(pos[1], "/") {
			return "", "", &UsageError{kind, kind + " show needs a name without \"/\", not empty or dot-only"}
		}
		return "show", pos[1], nil
	}
	return "", "", &UsageError{kind, kind + " needs list or show <name>"}
}

// checkFileUsage refuses --file anywhere but skills show, and an empty or
// dot-only path, before any server, token or request is involved.
func (s *settings) checkFileUsage(kind, verb string) error {
	if !s.fileSet {
		return nil
	}
	if verb != "show" {
		return &UsageError{kind, "--file belongs to skills show <name>"}
	}
	if contract.CheckArticleID(s.file) != nil {
		return &UsageError{kind, "--file needs a path, not an empty or dot-only one"}
	}
	return nil
}

// skillFile prints one file of a skill as its raw bytes: no cleaning, no added
// newline. The path travels as one escaped segment through the client.
func (c *commander) skillFile(ctx context.Context, cl *contract.ClientWithResponses, name string, proj *string) error {
	path := c.s.file
	raw, err := cl.GetSkillFile(ctx, name, path, &contract.GetSkillFileParams{Project: proj})
	b, err := contract.Answer(raw, err, contract.ParseGetSkillFileResponse,
		func(r *contract.GetSkillFileResponse) *[]byte {
			if !strings.HasPrefix(r.HTTPResponse.Header.Get("Content-Type"), "application/octet-stream") {
				return nil // not an API answer, such as a proxy's login page
			}
			return &r.Body
		})
	if err != nil {
		var ae *contract.APIError
		if errors.As(err, &ae) && ae.Code == "not_found" {
			ae.Hint = "List the skill's files with: goldie skills show " + contract.CleanLine(name) + "; if that fails too, list the skills with: goldie skills list."
		}
		return wordError(err, c.local)
	}
	_, err = c.out.Write(*b)
	return err
}

// skills runs goldie skills list|show. Pattern: search and lookup.
func (c *commander) skills(ctx context.Context, verb, name string) error {
	proj, err := c.project()
	if err != nil {
		return err
	}
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	if verb == "show" && c.s.fileSet {
		return c.skillFile(ctx, cl, name, projectParam(proj))
	}
	if verb == "show" {
		raw, err := cl.GetSkill(ctx, name, &contract.GetSkillParams{Project: projectParam(proj)})
		s, err := contract.Answer(raw, err, contract.ParseGetSkillResponse,
			func(r *contract.GetSkillResponse) *contract.Skill { return r.JSON200 })
		if err != nil {
			return wordError(missHint(err, skillMissHint, "List the skills with: goldie skills list, then use a name from its results."), c.local)
		}
		if c.s.json {
			return c.printJSON(s)
		}
		var b strings.Builder
		writeSkill(&b, *s)
		writeHint(&b, s.Hint)
		_, err = fmt.Fprint(c.out, b.String())
		return err
	}
	raw, err := cl.ListSkills(ctx, &contract.ListSkillsParams{Project: projectParam(proj)})
	l, err := contract.Answer(raw, err, contract.ParseListSkillsResponse,
		func(r *contract.ListSkillsResponse) *contract.SkillList { return r.JSON200 })
	if err != nil {
		return wordError(err, c.local)
	}
	if c.s.json {
		return c.printJSON(l)
	}
	var b strings.Builder
	if len(l.Skills) == 0 {
		b.WriteString("No skills.\n" + emptyNextStep)
	}
	for _, s := range l.Skills {
		writeSkill(&b, s)
	}
	if len(l.Skills) > 0 {
		b.WriteString("\nRead one with: goldie skills show <name>\n")
	}
	writeHint(&b, l.Hint)
	_, err = fmt.Fprint(c.out, b.String())
	return err
}

// The server's plain-miss hints for an unknown skill or agent (api/items.go);
// show words them with the CLI's list command, as lookup does for articles.
const (
	skillMissHint = "List the skills, then use a name from the list."
	agentMissHint = "List the agents, then use a name from the list."
	// emptyNextStep follows an empty list, which the server explains only for
	// an unregistered Project.
	emptyNextStep = "Check which Project this repository maps to with: goldie status\n"
)

// missHint rewords the server's plain-miss hint on a not_found; any other
// error, and any other not_found hint, passes through unchanged.
func missHint(err error, plain, reworded string) error {
	var ae *contract.APIError
	if errors.As(err, &ae) && ae.Code == "not_found" && ae.Hint == plain {
		ae.Hint = reworded
	}
	return err
}

// writeSkill prints a skill's name and one line per file; each server text is
// one cleaned line, so none can forge a line of its own.
func writeSkill(b *strings.Builder, s contract.Skill) {
	fmt.Fprintf(b, "%s\n", contract.CleanLine(s.Name))
	for _, f := range s.Files {
		fmt.Fprintf(b, "  %s  %d bytes\n", contract.CleanLine(f.Path), f.Size)
	}
}

// agents runs goldie agents list|show. Pattern: search and lookup.
func (c *commander) agents(ctx context.Context, verb, name string) error {
	proj, err := c.project()
	if err != nil {
		return err
	}
	cl, err := c.client(true)
	if err != nil {
		return err
	}
	if verb == "show" {
		raw, err := cl.GetAgent(ctx, name, &contract.GetAgentParams{Project: projectParam(proj)})
		a, err := contract.Answer(raw, err, contract.ParseGetAgentResponse,
			func(r *contract.GetAgentResponse) *contract.Agent { return r.JSON200 })
		if err != nil {
			return wordError(missHint(err, agentMissHint, "List the agents with: goldie agents list, then use a name from its results."), c.local)
		}
		if c.s.json {
			return c.printJSON(a)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n\n%s\n", contract.CleanLine(a.Name), strings.TrimRight(contract.CleanText(a.Description), "\n"))
		writeHint(&b, a.Hint)
		_, err = fmt.Fprint(c.out, b.String())
		return err
	}
	raw, err := cl.ListAgents(ctx, &contract.ListAgentsParams{Project: projectParam(proj)})
	l, err := contract.Answer(raw, err, contract.ParseListAgentsResponse,
		func(r *contract.ListAgentsResponse) *contract.AgentList { return r.JSON200 })
	if err != nil {
		return wordError(err, c.local)
	}
	if c.s.json {
		return c.printJSON(l)
	}
	var b strings.Builder
	if len(l.Agents) == 0 {
		b.WriteString("No agents.\n" + emptyNextStep)
	}
	for _, a := range l.Agents {
		fmt.Fprintf(&b, "%s  %s\n", contract.CleanLine(a.Name), contract.CleanLine(a.Description))
	}
	if len(l.Agents) > 0 {
		b.WriteString("\nRead one with: goldie agents show <name>\n")
	}
	writeHint(&b, l.Hint)
	_, err = fmt.Fprint(c.out, b.String())
	return err
}
