package boundedio

import (
	"errors"
	"fmt"
	"io"
)

var ErrTooLarge = errors.New("boundedio: stream is larger than its limit")

const MaxExpansion = 32

type Reader struct {
	R    io.Reader
	N    int64
	What string

	read int64
}

func Limit(r io.Reader, n int64, what string) *Reader {
	return &Reader{R: r, N: n, What: what}
}

func LimitExpansion(r io.Reader, compressed, fallback int64, what string) *Reader {
	n := fallback
	if compressed > 0 {
		n = compressed * MaxExpansion
	}
	return Limit(r, n, what)
}

func (b *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	if room := b.N - b.read + 1; int64(len(p)) > room {
		p = p[:room]
	}
	n, err := b.R.Read(p)
	b.read += int64(n)
	if b.read > b.N {
		return 0, b.fail()
	}
	return n, err
}

func (b *Reader) fail() error {
	return fmt.Errorf("%s exceeded %d bytes: %w", b.What, b.N, ErrTooLarge)
}

type Counter struct {
	R io.Reader
	n int64
}

func (c *Counter) Read(p []byte) (int, error) {
	n, err := c.R.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *Counter) Count() int64 { return c.n }

type Ratio struct {
	R     io.Reader
	Src   *Counter
	Max   int64
	Floor int64
	What  string

	read int64
}

func LimitRatio(decompressed io.Reader, src *Counter, what string) *Ratio {
	return &Ratio{R: decompressed, Src: src, Max: MaxExpansion, Floor: 1 << 20, What: what}
}

func (r *Ratio) Read(p []byte) (int, error) {
	n, err := r.R.Read(p)
	r.read += int64(n)

	max := r.Max
	if max <= 0 {
		max = MaxExpansion
	}

	allowed := r.Src.Count() * max
	if allowed < r.Floor {
		allowed = r.Floor
	}
	if r.read > allowed {
		return 0, fmt.Errorf(
			"%s expanded to %d bytes from %d compressed, over the %dx limit: %w",
			r.What, r.read, r.Src.Count(), max, ErrTooLarge)
	}
	return n, err
}
