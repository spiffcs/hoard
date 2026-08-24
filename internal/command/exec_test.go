package command

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

func pipeEnv(io.Writer) ui.Env { return ui.Env{Width: 80} }

func bufEnv(w io.Writer) *cli.Env {
	e := ui.Env{Width: 80}
	return &cli.Env{Out: w, Err: io.Discard, OutEnv: e, ErrEnv: e}
}

func execCmd(ctx context.Context, st *store.Store, args []string, jsonOut bool) (string, error) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	e := ui.Env{Width: 80}
	a := &app{
		store: st,
		env:   &cli.Env{Out: out, Err: errOut, OutEnv: e, ErrEnv: e, JSON: jsonOut},
	}

	root, _ := buildRoot(a, pipeEnv)

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

func execWatch(ctx context.Context, st *store.Store, args []string, jsonOut bool) error {
	_, err := execCmd(ctx, st, append([]string{"watch"}, args...), jsonOut)
	return err
}

func mustExec(t *testing.T, ctx context.Context, st *store.Store, args []string) string {
	t.Helper()
	out, err := execCmd(ctx, st, args, false)
	if err != nil {
		t.Fatalf("hoard %v: %v", args, err)
	}
	return out
}
