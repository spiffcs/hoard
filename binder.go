package main

// The binder commands: organizing the loose collection into labelled parts.

import (
	"fmt"
	"os"
	"strings"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func cmdBinder(st *store.Store, args []string) error {
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		return binderList(st)
	case "new":
		if len(args) != 1 {
			return fmt.Errorf("usage: hoard binder new NAME")
		}
		id, err := st.CreateBinder(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("Created binder #%d %q\n", id, strings.TrimSpace(args[0]))
		return nil
	case "rename":
		if len(args) != 2 {
			return fmt.Errorf("usage: hoard binder rename BINDER NEW-NAME")
		}
		b, err := st.BinderByRef(args[0])
		if err != nil {
			return err
		}
		if err := st.RenameBinder(b.ID, args[1]); err != nil {
			return err
		}
		fmt.Printf("Renamed binder %q to %q\n", b.Name, strings.TrimSpace(args[1]))
		return nil
	case "rm":
		if len(args) != 1 {
			return fmt.Errorf("usage: hoard binder rm BINDER")
		}
		b, err := st.BinderByRef(args[0])
		if err != nil {
			return err
		}
		if err := st.DeleteBinder(b.ID); err != nil {
			return err
		}
		fmt.Printf("Removed binder %q\n", b.Name)
		return nil
	default:
		return fmt.Errorf("unknown binder subcommand %q (want list|new|rename|rm)", sub)
	}
}

func binderList(st *store.Store) error {
	binders, err := st.ListBinders()
	if err != nil {
		return err
	}
	env := ui.Detect(os.Stdout)
	t := ui.Table{
		Env: env, Header: true,
		Cols: []ui.Col{
			{Title: "ID", Align: ui.Right, Style: env.Dim()},
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 8},
			{Title: "CARDS", Align: ui.Right},
			{Title: "VALUE", Align: ui.Right},
		},
	}
	for _, b := range binders {
		t.Add(ui.C(fmt.Sprint(b.ID)), ui.C(b.Name),
			ui.C(ui.Count(b.TotalCopies)), ui.C(ui.Money(b.Value)))
	}
	fmt.Print(t.Render())
	return nil
}
