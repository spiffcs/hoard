package collsource

import (
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
)

type Row struct {
	Quantity int
	Ident    scryfall.Identifier
	Name     string
	Finish   finish.Finish

	Condition string

	Binder string

	Kind string
}

func (r Row) Request() resolve.Request {
	return resolve.Request{Ident: r.Ident, Name: r.Name, Finish: r.Finish}
}

type Collection struct {
	Rows   []Row
	Format string

	Dropped map[string]int
}
