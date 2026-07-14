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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
//	(130/143 when a SIGINT/SIGTERM interrupted the run — see below)
//
// On a non-zero exit it prints {"error":{...}} to stderr. It is the only place
// that calls os.Exit-worthy logic; main is just os.Exit(cli.Execute()).
//
// Signals: the root context is cancelled on the first SIGINT/SIGTERM, which
// unwinds any in-flight subprocess (e.g. `furrow sync`'s git via
// exec.CommandContext) gracefully. Once cancelled, the default signal
// disposition is restored, so a SECOND Ctrl-C hard-kills a wedged process
// instead of being swallowed. When such a signal is what interrupted the run
// (the returned error is "sync-interrupted"), Execute returns 128+signal by
// Unix convention — 130 for SIGINT, 143 for SIGTERM — instead of the interior
// exit 3, keeping the error id/message (and its retryable meaning) unchanged. A
// deliberate sync-conflict is not a cancellation, so it keeps its exit 3.
func Execute() int {
	ctx, caught, stop := installSignalTrap(context.Background())
	defer stop()

	root := newRootCmd()
	// Board-config [alias] expansion (git-style): a leading token that names an
	// alias (not a builtin) is rewritten before cobra dispatch. A real command
	// always wins; any discovery error leaves args untouched.
	root.SetArgs(expandAlias(root, os.Args[1:]))
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
	// Remap a signal-caused interruption to 128+signal, leaving the envelope's
	// code field consistent with the process exit code.
	fe.Code = interruptedExitCode(fe, caught.Load())
	renderError(fe)
	return int(fe.Code)
}

// installSignalTrap wires SIGINT/SIGTERM to cancel the returned context and
// records which signal arrived first (caught stores its numeric value, 0 =
// none) so Execute can map it to a 128+signal exit code. After the first signal
// it restores the default disposition, so a SECOND Ctrl-C hard-kills a wedged
// process instead of being buffered. stop() detaches the handler and is
// idempotent; it also unblocks the watcher goroutine when no signal ever came.
func installSignalTrap(parent context.Context) (ctx context.Context, caught *atomic.Int64, stop func()) {
	ctx, cancel := context.WithCancel(parent)
	caught = &atomic.Int64{}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case s := <-sigCh:
			if sysSig, ok := s.(syscall.Signal); ok {
				caught.Store(int64(sysSig))
			}
			signal.Stop(sigCh) // restore default: a 2nd signal terminates hard
			cancel()           // unwind in-flight subprocess gracefully
		case <-done:
		}
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			signal.Stop(sigCh)
			close(done)
			cancel()
		})
	}
	return ctx, caught, stop
}

// interruptedExitCode remaps a signal-caused sync interruption to the Unix
// 128+signal convention (130 for SIGINT, 143 for SIGTERM) when a signal was
// caught. Every other outcome keeps its normal code — including a deliberate
// sync-conflict racing a signal, which is a definitive result, not a
// cancellation (see app.interruptError). caughtSig is 0 when no signal arrived.
func interruptedExitCode(fe *core.Error, caughtSig int64) core.Code {
	if caughtSig != 0 && fe.ID == "sync-interrupted" {
		return core.Code(128 + caughtSig)
	}
	return fe.Code
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
			"Code can edit the store cleanly.\n\n" +
			"Exit codes: 0 ok (an empty query result is still 0) · 1 a specifically requested\n" +
			"id was not found (e.g. show <id>) · 2 bad usage / validation (fix the args, do\n" +
			"not retry) · 3+ internal / IO (130/143 when a SIGINT/SIGTERM interrupted the\n" +
			"run — 128+signal by Unix convention). On a non-zero exit an\n" +
			"{\"error\":{code,id,message[,details][,candidates]}} object is written to stderr;\n" +
			"stdout stays pure data (JSON with --json), so piping stdout to jq is always clean.\n\n" +
			"The board's layout version gates writes, and it is an INPUT — an ordinary write\n" +
			"never raises it (only `furrow upgrade` does). Two ids say which side is stale,\n" +
			"and the exit code alone tells them apart:\n" +
			"  schema-upgrade-required (exit 2) the BOARD is behind this binary. It stays\n" +
			"                                   fully READABLE but is read-only until\n" +
			"                                   `furrow upgrade` runs.\n" +
			"  schema-too-new          (exit 3) the BINARY is behind the board — update\n" +
			"                                   furrow (in CI: bump the pin). Note this is a\n" +
			"                                   deliberate refusal that nonetheless exits 3:\n" +
			"                                   the fix is the binary, not the input.\n" +
			"Both carry details {board_schema, binary_schema}. `furrow board` reports the\n" +
			"state (schema_state / writable) WITHOUT failing — read it as a pre-flight rather\n" +
			"than provoking an error; `furrow lint` warns schema-outdated.",
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
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "output JSON to stdout (reads, mutations, and reports)")
	root.PersistentFlags().BoolVar(&flagNDJSON, "ndjson", false, "compact JSON, one value per line — honored wherever --json is")

	root.AddCommand(
		newInitCmd(),
		newAddCmd(),
		newLsCmd(),
		newShowCmd(),
		newNextCmd(),
		newRevisitCmd(),
		newSearchCmd(),
		newStatsCmd(),
		newBoardCmd(),
		newEditCmd(),
		newNoteCmd(),
		newAttachCmd(),
		newDoneCmd(),
		newMoveCmd(),
		newReorderCmd(),
		newRetitleCmd(),
		newSetCmd(),
		newValueCmd(),
		newEffortCmd(),
		newCheckCmd(),
		newDepCmd(),
		newParentCmd(),
		newLabelCmd(),
		newRepoCmd(),
		newReviewCmd(),
		newApplyCmd(),
		newSyncCmd(),
		newArchiveCmd(),
		newMigrateCmd(),
		newUpgradeCmd(),
		newLintCmd(),
		newConfigCmd(),
		newSchemaCmd(),
		newVersionCmd(),
		newUICmd(),
	)
	return root
}

// expandAlias rewrites args when the first arg names a board-config [alias],
// git-style: the alias's whitespace-split tokens replace it and the remaining
// args are appended (so `furrow triage -r sill` → `ls -s inbox,backlog -r sill`).
// It fires only when the first arg is not a flag and not a builtin command (a
// real command always wins — a shadowing alias is inert), and the alias resolves
// against the enclosing board's config. Any discovery/config error returns args
// unchanged; expansion never breaks furrow where a real command would work.
func expandAlias(root *cobra.Command, args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args
	}
	if isBuiltinCommand(root, args[0]) {
		return args
	}
	cwd, err := os.Getwd()
	if err != nil {
		return args
	}
	expansion, ok := app.DiscoverAliases(cwd)[args[0]]
	if !ok || strings.TrimSpace(expansion) == "" {
		return args
	}
	return append(strings.Fields(expansion), args[1:]...)
}

// isBuiltinCommand reports whether name is one of root's subcommands or an alias
// of one (e.g. `list` for `ls`, or the built-in `help`/`completion`).
func isBuiltinCommand(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return true
		}
		for _, a := range c.Aliases {
			if a == name {
				return true
			}
		}
	}
	return false
}

// aliasShadowProblems returns a lint warning for every [alias] whose name
// shadows a builtin command — the alias is inert (expansion checks builtins
// first), so this surfaces the dead config entry. Sorted by name for
// determinism.
func aliasShadowProblems(aliases map[string]string) []core.Problem {
	if len(aliases) == 0 {
		return nil
	}
	root := newRootCmd()
	names := make([]string, 0, len(aliases))
	for name := range aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	var ps []core.Problem
	for _, name := range names {
		if isBuiltinCommand(root, name) {
			ps = append(ps, core.Problem{Severity: core.SevWarn, Code: "alias-shadow", ID: "alias", Msg: fmt.Sprintf("alias %q shadows the builtin command; the builtin wins (the alias is inert)", name)})
		}
	}
	return ps
}

// unknownSubcommandErr is the validation error a parent command returns for an
// unrecognized subcommand: exit 2 with the known subcommand names in
// candidates, so an agent branches on the array instead of the exit-0 help
// prose cobra prints by default (the root's own unknown-command path already
// exits 2 — this gives a parent like `config` the same contract).
func unknownSubcommandErr(cmd *cobra.Command, sub string) error {
	var names []string
	for _, c := range cmd.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		names = append(names, c.Name())
	}
	sort.Strings(names)
	return &core.Error{
		Code:       core.CodeValidation,
		Msg:        fmt.Sprintf("unknown subcommand %q for %q (known: %s)", sub, cmd.CommandPath(), strings.Join(names, ", ")),
		Candidates: names,
	}
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
