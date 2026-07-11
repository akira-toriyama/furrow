// Package cli is the cobra adapter — furrow's command-line presentation layer.
// It parses flags, calls internal/app for every mutation/query, and renders the
// result (human table on a TTY, JSON with --json). It holds no task logic: that
// all lives in app/core. See docs/architecture.md.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/akira-toriyama/furrow/internal/version"
	"github.com/spf13/cobra"
)

// global flags
var (
	flagJSON   bool
	flagNDJSON bool
)

// Execute builds the root command, runs it, and maps the result to furrow's
// exit-code contract:
//
//	0 ok / 1 not-found|empty / 2 bad-usage|validation / 3+ internal|IO
//
// On a non-zero exit it prints {"error":{...}} to stderr. It is the only place
// that calls os.Exit-worthy logic; main is just os.Exit(cli.Execute()).
//
// Signals: the root context is cancelled on the first SIGINT/SIGTERM, which
// unwinds any in-flight subprocess (e.g. `furrow sync`'s git via
// exec.CommandContext) gracefully. Once cancelled, the default signal
// disposition is restored, so a SECOND Ctrl-C hard-kills a wedged process
// instead of being swallowed.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done() // first signal (or normal completion) — restore default so a
		stop()       // second signal terminates hard rather than being buffered
	}()

	root := newRootCmd()
	err := root.ExecuteContext(ctx)
	if err == nil {
		return int(core.CodeOK)
	}
	// app/core always return *core.Error; a bare error here is a cobra
	// usage/parse problem, which is a validation error by contract.
	fe := core.AsError(err)
	if fe == nil {
		fe = &core.Error{Code: core.CodeValidation, Msg: err.Error()}
	}
	renderError(fe)
	return int(fe.Code)
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "furrow",
		Short: "Repo-local plain-text task tracker (per-task JSON shards + markdown bodies)",
		Long: "furrow — a clonable, git-native plain-text task tracker: an alternative to\n" +
			"GitHub Projects/Issues that lives in a git repo (a shared central board or the\n" +
			"code repo itself).\n\n" +
			"Structured metadata lives in one .furrow/tasks/<id>.json shard per task\n" +
			"(deterministic, machine-written); long-form prose lives in .furrow/bodies/<id>.md\n" +
			"(hand-editable). Drive it from the CLI or the TUI (furrow ui). Both you and Claude\n" +
			"Code can edit the store cleanly.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Version holds the full human line (e.g. "furrow v1.2.3 (abc1234, ...)")
		// so `furrow --version` and the `version` subcommand render identically;
		// the template below prints it verbatim instead of cobra's default
		// "furrow version <x>" form.
		Version: version.Resolve().String(),
		// non-interactive by default: never prompt; the TUI is `furrow ui` only.
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: false},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "output JSON to stdout (read commands)")
	root.PersistentFlags().BoolVar(&flagNDJSON, "ndjson", false, "output newline-delimited JSON, one task per line (list commands)")

	root.AddCommand(
		newInitCmd(),
		newAddCmd(),
		newLsCmd(),
		newShowCmd(),
		newNextCmd(),
		newRevisitCmd(),
		newBoardCmd(),
		newEditCmd(),
		newAttachCmd(),
		newDoneCmd(),
		newMoveCmd(),
		newReorderCmd(),
		newRetitleCmd(),
		newValueCmd(),
		newEffortCmd(),
		newCheckCmd(),
		newDepCmd(),
		newLabelCmd(),
		newRepoCmd(),
		newApplyCmd(),
		newSyncCmd(),
		newArchiveCmd(),
		newMigrateCmd(),
		newLintCmd(),
		newConfigCmd(),
		newSchemaCmd(),
		newVersionCmd(),
		newUICmd(),
	)
	return root
}

// openApp discovers the .furrow store from the current directory. Any
// discovery-time scope warnings (e.g. a central board activated with no
// enclosing git repo for an auto label) go to stderr, so stdout stays pure data.
func openApp() (*app.App, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, core.Internalf("", "getwd: %v", err)
	}
	a, err := app.Open(cwd)
	if err != nil {
		return nil, err
	}
	for _, w := range a.ScopeWarnings {
		fmt.Fprintln(errOut, w)
	}
	return a, nil
}

// renderError prints a structured error object to stderr (never stdout, so a
// caller piping stdout to jq is unaffected).
func renderError(fe *core.Error) {
	type errBody struct {
		Code    int    `json:"code"`
		ID      string `json:"id,omitempty"`
		Msg     string `json:"message"`
		Details any    `json:"details,omitempty"`
		// Candidates carries the concrete alternatives when an input almost
		// resolved (ambiguous repo short name, the -l did-you-mean guard), so
		// agents branch on the array and never regex the message prose.
		Candidates []string `json:"candidates,omitempty"`
	}
	type envelope struct {
		Error errBody `json:"error"`
	}
	env := envelope{Error: errBody{Code: int(fe.Code), ID: fe.ID, Msg: fe.Msg, Details: fe.Details, Candidates: fe.Candidates}}
	// Errors are always JSON on stderr — machine-readable for Claude/scripts,
	// still readable for humans.
	b := mustJSON(env)
	fmt.Fprintln(errOut, string(b))
}
