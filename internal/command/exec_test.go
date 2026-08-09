package command

// Test helpers for driving the command tree.
//
// Tests that used to call a cmdXxx function directly now go through real cobra
// dispatch, so they cover argument parsing and the --json policy as well as the
// command body. They can also read what the command wrote, which direct calls
// could not: output went to os.Stdout, which is why most of the root package's
// commands had no output assertions at all.

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// pipeEnv is the shape a piped stream gets: fixed width, no color, nothing
// truncated, so assertions compare plain text.
func pipeEnv(io.Writer) ui.Env { return ui.Env{Width: 80} }

// bufEnv is a cli.Env writing to w, for the tests that call a run function
// directly instead of dispatching through the tree. Narration is discarded:
// these tests assert on the piped record, and stderr is conversation.
func bufEnv(w io.Writer) *cli.Env {
	e := ui.Env{Width: 80}
	return &cli.Env{Out: w, Err: io.Discard, OutEnv: e, ErrEnv: e}
}

// execCmd runs args through the real tree and returns what reached stdout.
//
// The store is injected rather than opened by PersistentPreRunE, so a test
// never touches $HOARD_DB or the developer's real collection.
func execCmd(ctx context.Context, st *store.Store, args []string, jsonOut bool) (string, error) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	e := ui.Env{Width: 80}
	a := &app{
		store: st,
		env:   &cli.Env{Out: out, Err: errOut, OutEnv: e, ErrEnv: e, JSON: jsonOut},
	}

	// The same builder the binary calls, so the global flags and the help
	// renderer are the real ones rather than a restatement that can drift.
	root, _ := buildRoot(a, pipeEnv)
	// Only the policy check runs here — the store is already open, so the
	// production hook (which opens one) must not.
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.CheckJSON(cmd, jsonOut)
	}

	if jsonOut {
		args = append([]string{"--json"}, args...)
	}
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	return out.String(), err
}

// execWatch is execCmd scoped to the watch subtree, so the existing tests read
// as they did while now exercising real dispatch.
func execWatch(ctx context.Context, st *store.Store, args []string, jsonOut bool) error {
	_, err := execCmd(ctx, st, append([]string{"watch"}, args...), jsonOut)
	return err
}

// mustExec fails the test if the command errors, and returns its stdout.
func mustExec(t *testing.T, ctx context.Context, st *store.Store, args []string) string {
	t.Helper()
	out, err := execCmd(ctx, st, args, false)
	if err != nil {
		t.Fatalf("hoard %v: %v", args, err)
	}
	return out
}
