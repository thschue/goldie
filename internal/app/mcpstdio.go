package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/contract"
	gmcp "github.com/thschue/goldie/internal/mcp"
	"github.com/thschue/goldie/internal/platform"
)

// MCPUsage is the one-line usage of the mcp role, without the program name.
const MCPUsage = "mcp --transport stdio [--addr URL] [--token TOKEN]"

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
// message, so a bad one fails at startup. Nothing but MCP messages is ever
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
	addr, token, err := settings.Resolve(env)
	if err != nil {
		return fmt.Errorf("invalid client settings: %w", err)
	}
	client, err := contract.NewAPIClient(addr, token, platform.Version, env.Client)
	if err != nil {
		return fmt.Errorf("invalid client settings: %w", err)
	}
	err = gmcp.New(client).Run(ctx, &sdk.IOTransport{Reader: in, Writer: out})
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
