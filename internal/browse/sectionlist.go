package browse

type section struct {
	rowStart, count int
	curStart, span  int
}

type sectionList []section

func newSectionList(counts ...int) sectionList {
	out := make(sectionList, len(counts))
	row, cur := 0, 0
	for i, n := range counts {
		out[i] = section{rowStart: row, count: n, curStart: cur, span: max(n, 1)}
		row += n
		cur += out[i].span
	}
	return out
}

func (l sectionList) counts() []int {
	out := make([]int, len(l))
	for i, s := range l {
		out[i] = s.count
	}
	return out
}

func (l sectionList) rows() int {
	n := 0
	for _, s := range l {
		n += s.count
	}
	return n
}

func (l sectionList) cursorSlots() int {
	n := 0
	for _, s := range l {
		n += s.span
	}
	return n
}

func (l sectionList) cursorPos(cursor int) (sec, idx int) {
	cur := min(max(cursor, 0), max(l.cursorSlots()-1, 0))
	for i := len(l) - 1; i >= 0; i-- {
		if cur >= l[i].curStart {
			return i, min(cur-l[i].curStart, max(l[i].count-1, 0))
		}
	}
	return 0, 0
}

func (l sectionList) firstCursor() int {
	for _, s := range l {
		if s.count > 0 {
			return s.curStart
		}
	}
	return 0
}

func (l sectionList) budgets(pool, cursorSec int) []int {
	return sectionBudgets(l.counts(), pool, cursorSec)
}

func (l sectionList) scrollIntoView(offsets []int, budgets []int, cursor int) {
	if l.rows() > 0 {
		sec, idx := l.cursorPos(cursor)
		if b := budgets[sec]; b > 0 {
			if idx < offsets[sec] {
				offsets[sec] = idx
			}
			if idx >= offsets[sec]+b {
				offsets[sec] = idx - b + 1
			}
		}
	}
	for i := range l {
		offsets[i] = clampOffset(offsets[i], l[i].count, budgets[i])
	}
}

func (l sectionList) jump(cursor, dir int) (int, bool) {
	cur, _ := l.cursorPos(cursor)
	if i := cur + dir; i >= 0 && i < len(l) {
		return l[i].curStart, true
	}
	return cursor, false
}

func clampOffset(off, count, budget int) int {
	return min(max(off, 0), max(count-budget, 0))
}
