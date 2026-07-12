package cli

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/internal/core"
)

// TestInterruptedExitCode pins the 128+signal remap: a signal-caused sync
// interruption becomes 130 (SIGINT) / 143 (SIGTERM); every other outcome —
// including a sync-conflict racing a signal, and an interruption with no signal
// — keeps its normal exit code.
func TestInterruptedExitCode(t *testing.T) {
	const (
		sigint  = int64(syscall.SIGINT)  // 2 -> 130
		sigterm = int64(syscall.SIGTERM) // 15 -> 143
	)
	cases := []struct {
		name   string
		fe     *core.Error
		caught int64
		want   core.Code
	}{
		{"sigint interrupts sync", &core.Error{Code: core.CodeInternal, ID: "sync-interrupted"}, sigint, 130},
		{"sigterm interrupts sync", &core.Error{Code: core.CodeInternal, ID: "sync-interrupted"}, sigterm, 143},
		{"no signal keeps exit 3", &core.Error{Code: core.CodeInternal, ID: "sync-interrupted"}, 0, core.CodeInternal},
		{"conflict racing signal stays exit 3", &core.Error{Code: core.CodeInternal, ID: "sync-conflict"}, sigint, core.CodeInternal},
		{"unrelated error with signal keeps its code", &core.Error{Code: core.CodeValidation, ID: "bad-input"}, sigterm, core.CodeValidation},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := interruptedExitCode(c.fe, c.caught); got != c.want {
				t.Errorf("interruptedExitCode(id=%q code=%d, caught=%d) = %d, want %d",
					c.fe.ID, c.fe.Code, c.caught, got, c.want)
			}
		})
	}
}

// TestInstallSignalTrapRealSignal fires an actual SIGINT at this process and
// asserts the trap cancels the context and records the signal number — the
// plumbing behind the 130/143 exit codes, verified end-to-end without a
// heavyweight real-git sync.
func TestInstallSignalTrapRealSignal(t *testing.T) {
	ctx, caught, stop := installSignalTrap(context.Background())
	defer stop()

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("kill self with SIGINT: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("context was not cancelled within 2s of SIGINT")
	}
	if got := caught.Load(); got != int64(syscall.SIGINT) {
		t.Fatalf("caught signal = %d, want %d (SIGINT)", got, int64(syscall.SIGINT))
	}
	// The mapping then yields 130 for a sync interruption caused by this SIGINT.
	if got := interruptedExitCode(&core.Error{Code: core.CodeInternal, ID: "sync-interrupted"}, caught.Load()); got != 130 {
		t.Errorf("exit code after SIGINT = %d, want 130", got)
	}
}

// TestInstallSignalTrapStopNoSignal verifies stop() unblocks the watcher and
// leaves caught at 0 when no signal is delivered (the normal-completion path),
// and that stop() is idempotent.
func TestInstallSignalTrapStopNoSignal(t *testing.T) {
	_, caught, stop := installSignalTrap(context.Background())
	stop()
	stop() // idempotent: a second call must not panic or double-close
	if got := caught.Load(); got != 0 {
		t.Errorf("caught = %d with no signal delivered, want 0", got)
	}
}
