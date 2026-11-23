package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/spf13/pflag"
)

// Run runs one client command for the tests, parsing args as the dispatcher in
// cmd/goldie does: pflag reads the flags GNU style, before or after the words.
func Run(ctx context.Context, args []string, env Env) error {
	if len(args) == 0 || !isCommand(args[0]) {
		return errors.New("usage: goldie " + strings.Join(Commands, " | "))
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	exec := Command(args[0], fs)
	pf := pflag.NewFlagSet(args[0], pflag.ContinueOnError)
	pf.SetOutput(io.Discard)
	pf.AddGoFlagSet(fs)
	if err := pf.Parse(args[1:]); err != nil {
		return &UsageError{Command: args[0], Msg: "unknown flag, or a flag without its value"}
	}
	return exec(ctx, pf.Args(), env)
}
