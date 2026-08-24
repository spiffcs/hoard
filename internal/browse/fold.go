package browse

import (
	"slices"
	"strconv"
	"strings"
)

const (
	foldOpenMark = "▾"
	foldShutMark = "▸"
)

func (m *Model) loadFoldState() {
	s, err := m.store.Settings()
	if err != nil {
		return
	}
	m.collapsed = map[int64]bool{}
	for _, part := range strings.Split(s[setSidebarCollapsed], ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			m.collapsed[id] = true
		}
	}
}

func (m *Model) persistFoldState() {
	ids := make([]string, 0, len(m.collapsed))
	for id, folded := range m.collapsed {
		if folded {
			ids = append(ids, strconv.FormatInt(id, 10))
		}
	}
	slices.Sort(ids)
	if err := m.store.SaveSettings(
		map[string]string{setSidebarCollapsed: strings.Join(ids, ",")}); err != nil {
		m.status, m.statusErr = "saving fold state: "+err.Error(), true
	}
}

func (m *Model) setFolded(id int64, folded bool) {
	if m.collapsed == nil {
		m.collapsed = map[int64]bool{}
	}
	m.collapsed[id] = folded
}

func (m *Model) foldTarget() *container {
	sel := m.selectedContainer()
	if sel == nil {
		return nil
	}
	if sel.Kind == kindFolder {
		return sel
	}
	if sel.parentID == 0 {
		return nil
	}
	if i := m.containerIndexOf(sel.parentID); i >= 0 {
		return &m.containers[i]
	}
	return nil
}

func (m *Model) toggleFold() {
	if m.focus != paneContainers {
		return
	}
	target := m.foldTarget()
	if target == nil {
		m.status, m.statusErr = "only folders fold · select a folder or a deck inside one", true
		return
	}
	id, name := target.ID, target.Name
	m.setFolded(id, !m.collapsed[id])
	folded := m.collapsed[id]

	if err := m.loadContainers(); err != nil {
		m.setError(err)
		return
	}
	if i := m.containerIndexOf(id); i >= 0 {
		if err := m.selectContainer(i); err != nil {
			m.setError(err)
			return
		}
		m.displacedContainer = 0
	}
	m.persistFoldState()
	if folded {
		m.status, m.statusErr = "folded "+name, false
		return
	}
	m.status, m.statusErr = "opened "+name, false
}
