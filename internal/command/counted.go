package command

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/store"
)

func countedVerb(counted bool) string {
	if counted {
		return "include"
	}
	return "exclude"
}

func setCounted(st *store.Store, env *cli.Env, noun, name string, id int64, counted bool) error {
	if err := st.SetContainerCounted(id, counted); err != nil {
		return err
	}
	if counted {
		fmt.Fprintf(env.Out, "%s %q counts toward your collection again\n", noun, name)
		return nil
	}
	fmt.Fprintf(env.Out, "%s %q is no longer counted toward your collection\n", noun, name)
	return nil
}

func binderCounted(st *store.Store, env *cli.Env, args []string, counted bool) error {
	verb := countedVerb(counted)
	if len(args) != 1 {
		return cli.Usagef("binder %s takes one binder: hoard binder %s BINDER", verb, verb)
	}
	b, err := st.BinderByRef(args[0])
	if err != nil {
		return err
	}
	return setCounted(st, env, "Binder", b.Name, b.ID, counted)
}

func deckCounted(st *store.Store, env *cli.Env, args []string, counted bool) error {
	verb := countedVerb(counted)
	if len(args) != 1 {
		return cli.Usagef("deck %s takes one deck: hoard deck %s DECK", verb, verb)
	}
	d, err := st.DeckByRef(args[0])
	if err != nil {
		return err
	}
	return setCounted(st, env, "Deck", d.Name, d.ID, counted)
}
