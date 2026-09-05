package browse

import "fmt"

type pager struct {
	page  int
	size  int
	total int
}

func (p pager) maxPage() int {
	if p.total <= 0 {
		return 0
	}
	return (p.total - 1) / p.size
}

func (p pager) clamped() int { return min(max(p.page, 0), p.maxPage()) }

func (p pager) bounds() (lo, hi int) {
	lo = min(p.clamped()*p.size, p.total)
	return lo, min(lo+p.size, p.total)
}

func (p pager) turn(dir int) (next int, edge string) {
	next = min(max(p.page+dir, 0), p.maxPage())
	if next != p.page {
		return next, ""
	}
	switch {
	case p.maxPage() == 0:
		return p.page, "one page here"
	case dir > 0:
		return p.page, "last page"
	}
	return p.page, "first page"
}

func (p pager) rangePhrase() string {
	return fmt.Sprintf("page %d/%d · rows %d–%d of %d",
		p.page+1, p.maxPage()+1, p.page*p.size+1,
		min((p.page+1)*p.size, p.total), p.total)
}
