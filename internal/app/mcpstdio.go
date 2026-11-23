package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/contract/project"
	gmcp "github.com/thschue/goldie/internal/mcp"
	"github.com/thschue/goldie/internal/platform"
)

// MCPUsage is the one-line usage of the mcp role, without the program name.
const MCPUsage = "mcp --transport stdio [--addr URL | --dir PATH] [--token TOKEN]"

// mcpTokenNote recommends the token variables over --token: its value shows in
// the process list (ps), and an MCP client saves it in its config.
const mcpTokenNote = "Prefer " + cli.EnvToken + " or " + cli.EnvToken + "_FILE to --token: a --token value shows in the process list (ps) and is saved in the MCP client's config"

// errTransport is the usage error for a missing or unknown --transport. The
// value given is never echoed: it may hold control characters.
var errTransport = errors.New("--transport must be stdio, the only transport so far\nusage: goldie " + MCPUsage + "\n" + mcpTokenNote)

// RunMCP runs `goldie mcp`: the MCP server over stdio (in and out), reaching
// the API through contract's hardened client, with the CLI's client settings
// from args and env. It returns nil when the client ends the session by
// closing in, and when ctx is cancelled. Settings are checked before the first
// message, so a bad one fails at startup. The default Project is detected
// once, at startup, as the CLI detects it, and worded by cli.ProjectWords; a
// Project problem does not stop the session. Nothing but MCP messages is ever
// written to out.
func RunMCP(ctx context.Context, args []string, env cli.Env, in io.ReadCloser, out io.WriteCloser) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	transport := fs.String("transport", "", "the MCP transport: stdio")
	settings := cli.BindClientFlags(fs)
	if err := fs.Parse(args); err != nil {
		// flag's message echoes the argument, which may hold control characters.
		return errors.New("unknown flag, or a flag without its value\nusage: goldie " + MCPUsage + "\n" + mcpTokenNote)
	}
	if fs.NArg() != 0 {
		return errors.New("usage: goldie " + MCPUsage + "\n" + mcpTokenNote)
	}
	if *transport != "stdio" {
		return errTransport
	}
	addr, token, stop, err := settings.Open(ctx, env)
	if err != nil {
		return fmt.Errorf("invalid client settings: %w", err)
	}
	defer stop()
	client, err := contract.NewAPIClient(addr, token, platform.Version, env.Client)
	if err != nil {
		return fmt.Errorf("invalid client settings: %w", err)
	}
	// The default Project is detected here, once, in the CLI's order without
	// --project: GOLDIE_PROJECT, .goldie/config.yaml, then the git remotes.
	// Only this stdio role detects; the server never does (depguard).
	var detected project.Detected
	dir, err := os.Getwd()
	if err != nil {
		err = errors.New("cannot read the working directory")
	} else {
		envValue, _ := env.Lookup(cli.EnvProject)
		detected, err = project.Detect(dir, "", envValue)
	}
	opts := projectOptions(cli.ProjectWords(detected, err))
	if settings.Local() {
		opts = append(opts, gmcp.Local())
	}
	err = gmcp.New(client, opts...).Run(ctx, &sdk.IOTransport{Reader: in, Writer: out})
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// projectOptions hands the Project detected at startup to the MCP server as
// plain strings: the default and its source, the problem that stops it, or the
// rejected remote. Over HTTP (RunMCPHTTP) nothing is detected.
func projectOptions(p cli.SessionProject) []gmcp.Option {
	switch {
	case p.Problem != "":
		return []gmcp.Option{gmcp.WithProjectProblem(p.Problem)}
	case p.ID != "":
		return []gmcp.Option{gmcp.WithProject(p.ID, p.Source)}
	case p.Rejected != "":
		return []gmcp.Option{gmcp.WithRejectedRemote(p.Rejected)}
	}
	return nil
}
