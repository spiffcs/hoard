package browse

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
)

func nameFinishKey(name string, fin finish.Finish) string {
	return name + "|" + fin.String()
}

func (m *Model) labelHeldIn(rows []card) error {
	places, err := m.store.WhereHeld()
	if err != nil {
		return err
	}
	byCard := make(map[string][]string, len(rows))
	for _, p := range places {
		key := nameFinishKey(p.Name, p.Finish)
		byCard[key] = append(byCard[key], p.ContainerName)
	}
	for i := range rows {
		rows[i].HeldIn = heldInLabel(byCard[nameFinishKey(rows[i].Name, rows[i].Finish)])
	}
	return nil
}

func heldInLabel(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return fmt.Sprintf("%s +%d", names[0], len(names)-1)
}
