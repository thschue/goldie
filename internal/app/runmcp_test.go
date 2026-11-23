package app

import (
	"context"
	"flag"
	"io"

	"github.com/spf13/pflag"

	"github.com/thschue/goldie/internal/cli"
)

// RunMCP runs goldie mcp over stdio for the tests, parsing args as the
// dispatcher in cmd/goldie does: pflag reads the flags GNU style.
func RunMCP(ctx context.Context, args []string, env cli.Env, in io.ReadCloser, out io.WriteCloser) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	run := BindMCP(fs)
	pf := pflag.NewFlagSet("mcp", pflag.ContinueOnError)
	pf.SetOutput(io.Discard)
	pf.AddGoFlagSet(fs)
	if err := pf.Parse(args); err != nil {
		return &cli.UsageError{Command: "mcp", Msg: "unknown flag, or a flag without its value"}
	}
	return run(ctx, pf.Args(), env, in, out)
}
