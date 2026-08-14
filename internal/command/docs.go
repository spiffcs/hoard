package command

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/ui"
)

func DocTree() *cobra.Command {
	fixed := func(io.Writer) ui.Env { return ui.Env{Width: 80} }
	a := &app{env: &cli.Env{
		Out: io.Discard, Err: io.Discard,
		OutEnv: fixed(io.Discard), ErrEnv: fixed(io.Discard),
	}}
	root, _ := buildRoot(a, fixed)

	root.DisableAutoGenTag = true
	return root
}
