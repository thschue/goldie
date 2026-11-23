package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/thschue/goldie/internal/app"
	"github.com/thschue/goldie/internal/cli"
	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// The two groups of the root help.
const (
	groupRun = "run"
	groupAsk = "ask"
)

func init() {
	// Help lists the commands in the order they are added.
	cobra.EnableCommandSorting = false
}

// usageErr is a command used wrongly; main prints it to stderr and exits 2.
type usageErr struct {
	path string // "goldie server", or "goldie" for the root
	msg  string
}

func (e *usageErr) Error() string { return e.msg }

// newUsageErr cleans msg, which may echo control characters, to one line.
func newUsageErr(path, msg string) *usageErr {
	return &usageErr{path: path, msg: contract.CleanLine(msg)}
}

// misuse turns the client roles' usage errors into usageErr.
func misuse(err error) error {
	var ue *cli.UsageError
	if errors.As(err, &ue) {
		msg := ue.Msg
		if rest, ok := strings.CutPrefix(msg, "usage: "); ok {
			msg = "wrong arguments: " + rest
		}
		return newUsageErr("goldie "+ue.Command, msg)
	}
	return err
}

// noArgs is the Args check of a command that takes no positional words.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return newUsageErr(cmd.CommandPath(), cmd.Name()+" takes no arguments")
	}
	return nil
}

// helpTemplate is the help of a command: its usage, flags and examples.
const helpTemplate = `{{.Long}}

Usage:
  {{.UseLine}}
{{if .Example}}
Examples:
{{.Example}}
{{end}}{{if .HasAvailableLocalFlags}}
Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}
{{end}}`

// rootHelpTemplate lists the commands in the two groups.
const rootHelpTemplate = `{{.Long}}

Usage:
  {{.CommandPath}} <command> [flags]
{{range $g := .Groups}}
{{$g.Title}}:{{range $.Commands}}{{if and (eq .GroupID $g.ID) .IsAvailableCommand}}
  {{rpad .Name .NamePadding}} {{.Short}}{{end}}{{end}}
{{end}}
Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}

Run '{{.CommandPath}} <command> --help' for a command's flags and examples.
`

// newRoot builds the command tree: the server roles run from this package, the
// client roles from internal/cli and internal/app. Help goes to out.
func newRoot(out io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:               "goldie",
		Short:             "Golden-path knowledge, just in time",
		Long:              "Goldie serves golden-path knowledge just in time to people and agents.",
		SilenceUsage:      true,
		SilenceErrors:     true,
		DisableAutoGenTag: true,
	}
	root.SetOut(out)
	root.SetErr(io.Discard)
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return newUsageErr(cmd.CommandPath(), err.Error())
	})
	root.AddGroup(
		&cobra.Group{ID: groupRun, Title: "Run a server"},
		&cobra.Group{ID: groupAsk, Title: "Ask a server"},
	)
	root.AddCommand(serverCmd(out), validateCmd(), healthCmd())
	root.AddCommand(clientCmds(out)...)
	root.AddCommand(mcpCmd())
	root.AddCommand(helpCmd(root))
	for _, c := range root.Commands() {
		if c.Name() != "help" {
			c.SetHelpTemplate(helpTemplate)
		}
	}
	root.SetHelpTemplate(rootHelpTemplate)
	return root
}

// dashedWord returns the first single-dash word of more than one letter, such
// as -dev, which someone used for a long flag.
func dashedWord(args []string) string {
	for _, a := range args {
		if len(a) > 2 && a[0] == '-' && a[1] != '-' && !strings.ContainsAny(a, "= ") {
			return a
		}
	}
	return ""
}

func firstWord(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// helpCmd is `goldie help [command]`, the text of `goldie <command> --help`.
func helpCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Show help for a command",
		Long:  "Show the usage, flags and examples of a command.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				root.InitDefaultHelpFlag() // so the flag list matches `goldie --help`
				return root.Help()
			}
			if len(args) > 1 {
				return newUsageErr("goldie help", "help takes one command")
			}
			for _, c := range root.Commands() {
				if c.Name() == args[0] && c.IsAvailableCommand() {
					c.InitDefaultHelpFlag() // so the flag list matches `goldie <command> --help`
					return c.Help()
				}
			}
			return newUsageErr(root.CommandPath(), fmt.Sprintf("unknown command %q", args[0]))
		},
	}
}

func serverCmd(out io.Writer) *cobra.Command {
	var dev bool
	cmd := &cobra.Command{
		Use:     "server [--dev [dir]]",
		Short:   "Serve the API, UI and MCP over HTTP",
		GroupID: groupRun,
		Long: "Run the goldie server, configured by GOLDIE_* variables (docs/reference/configuration.md).\n" +
			"With --dev it serves dir (else " + platform.EnvContentDir + ", else the working directory)\n" +
			"on 127.0.0.1 with a random token and prints the export line for the client commands.\n" +
			"It ignores " + platform.EnvAddr + ", " + platform.EnvAdminAddr + " and the API token variables.",
		Example: "  goldie server\n" +
			"  eval \"$(goldie server --dev ./content)\"",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 || (len(args) == 1 && !dev) {
				return newUsageErr(cmd.CommandPath(), "server takes a directory only with --dev")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if dev {
				return runDev(args, out)
			}
			return runServer(out)
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "serve a directory on loopback with a random token")
	return cmd
}

func validateCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "validate [dir]",
		Short:   "Check a content directory for errors and warnings",
		GroupID: groupRun,
		Long:    "Validate the content in dir (else " + platform.EnvContentDir + "). Warnings go to stderr; it exits 1 on an error.",
		Example: "  goldie validate ./content",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return newUsageErr(cmd.CommandPath(), "validate takes one directory")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, _ := os.LookupEnv(platform.EnvContentDir)
			if len(args) == 1 {
				dir = args[0]
			}
			warnings, err := app.ValidateDirWarnings(context.Background(), dir)
			for _, w := range warnings {
				fmt.Fprintln(os.Stderr, "goldie: warning:", w)
			}
			return err
		},
	}
}

func healthCmd() *cobra.Command {
	var ready bool
	cmd := &cobra.Command{
		Use:     "health [--ready]",
		Short:   "Probe the local admin port",
		GroupID: groupRun,
		Long:    "Probe /livez, or /readyz with --ready, on this machine's admin port. It exits 0 on a 200.",
		Example: "  goldie health\n" +
			"  goldie health --ready",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHealth(ready)
		},
	}
	cmd.Flags().BoolVar(&ready, "ready", false, "check /readyz instead of /livez")
	return cmd
}

// clientHelp is what each client command says about itself.
var clientHelp = map[string]struct{ use, short, long, example string }{
	"search": {
		"search <words> [flags]", "Find articles for some words",
		"List the articles that match the words, best first.",
		"  goldie search kubernetes deployment\n  goldie search --json ingress",
	},
	"lookup": {
		"lookup <id> [flags]", "Read one article by its id",
		"Print one article. Find ids with goldie search.",
		"  goldie lookup platform/deploy",
	},
	"status": {
		"status [flags]", "Check that a server answers and accepts the token",
		"Report whether the server answers, whether it accepts the token and which\nProject the repository maps to.",
		"  goldie status\n  goldie status --addr http://127.0.0.1:8080",
	},
	"init": {
		"init [--project ID] [flags]", "Write .goldie/config.yaml for this repository",
		"Write .goldie/config.yaml at the repository root with the Project found from\nits git remotes, and say whether the server holds it. It never overwrites.",
		"  goldie init\n  goldie init --project github.com/acme/shop",
	},
	"version": {
		"version [flags]", "Show the client and server versions",
		"Print the version of this client and, when a server answers, of the server.",
		"  goldie version\n  goldie version --json",
	},
}

// clientCmds builds the client commands of internal/cli; pflag parses their
// stdlib flags GNU style.
func clientCmds(out io.Writer) []*cobra.Command {
	var cmds []*cobra.Command
	for _, name := range cli.Commands {
		h := clientHelp[name]
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		exec := cli.Command(name, fs)
		cmd := &cobra.Command{
			Use:     h.use,
			Short:   h.short,
			Long:    h.long + "\n\n" + cli.TokenNote,
			Example: h.example,
			GroupID: groupAsk,
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
				defer stop()
				env, flush, err := clientEnv(out, platform.RoleCLI)
				if err != nil {
					return err
				}
				defer flush()
				return misuse(exec(ctx, args, env))
			},
		}
		cmd.Flags().AddGoFlagSet(fs)
		cmds = append(cmds, cmd)
	}
	return cmds
}

func mcpCmd() *cobra.Command {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	exec := app.BindMCP(fs)
	cmd := &cobra.Command{
		Use:     "mcp --transport stdio [flags]",
		Short:   "Serve MCP on stdin and stdout for an agent",
		GroupID: groupAsk,
		Long:    "Serve MCP over stdin and stdout, for an agent that starts goldie. It takes the client flags.\n\n" + app.MCPTokenNote,
		Example: "  goldie mcp --transport stdio --addr http://127.0.0.1:8080",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Until the client closes stdin or a SIGTERM or SIGINT arrives.
			// Errors go to stderr through main: stdout carries only MCP messages.
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			env, flush, err := clientEnv(io.Discard, platform.RoleMCP)
			if err != nil {
				return err
			}
			defer flush()
			return misuse(exec(ctx, args, env, os.Stdin, os.Stdout))
		},
	}
	cmd.Flags().AddGoFlagSet(fs)
	return cmd
}
