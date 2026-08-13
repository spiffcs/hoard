// Package boundedio caps how many bytes a stream may produce, and fails when
// the cap is reached rather than reporting the end of the data.
//
// It exists for the decompressed side of the bulk downloads. A gzip or 7z
// stream is a promise about size that the sender makes and the receiver cannot
// check until it has already paid: 4 MB of input expanded to 32 MB of card
// JSON in the measurement this package's limits are drawn from, and a stream
// built to be hostile reaches ratios near gzip's 1032:1 ceiling. Nothing on
// the wire says which one is arriving.
//
// WHY NOT io.LimitReader, WHICH IS RIGHT THERE. Because it returns io.EOF at
// the limit, and every consumer downstream reads EOF as "that was all of it".
// A truncated card bundle would load as a *complete catalog missing most of
// the cards* — no error, no warning, and a collection that silently fails to
// resolve half of what the user owns. That is a worse outcome than the
// exhaustion this is defending against, because exhaustion is loud and a
// short catalog is not. The failure has to be an error, and it has to name
// what was too large.
package boundedio

import (
	"errors"
	"fmt"
	"io"
)

// ErrTooLarge is returned by Reader once its limit is exceeded. Callers that
// want to tell a hostile stream from a genuinely grown one can test for it.
var ErrTooLarge = errors.New("boundedio: stream is larger than its limit")

// MaxExpansion bounds decompressed output as a multiple of the compressed size
// the source declared.
//
// 32 against a measured 8.00 — the real Scryfall default_cards bundle, taken by
// decompressing a 4 MB range of it on 2026-08-13 (4,000,001 bytes in,
// 32,001,616 out). Four times the observed ratio leaves room for the data to
// change shape without anyone having to revisit this, and is still thirty times
// below what a stream built to expand can reach.
//
// Raise this if a legitimate download ever trips it. Do not remove it: the
// number is a bound on damage, not a prediction about card data.
const MaxExpansion = 32

// Reader reads from R and fails once it has produced more than N bytes.
type Reader struct {
	R    io.Reader
	N    int64
	What string // named in the error, so a failure says which download it was

	read int64
}

// Limit returns a Reader over r that permits at most n bytes.
func Limit(r io.Reader, n int64, what string) *Reader {
	return &Reader{R: r, N: n, What: what}
}

// LimitExpansion returns a Reader permitting MaxExpansion bytes of output per
// byte of the declared compressed size.
//
// A non-positive compressed size means the source did not say — no
// Content-Length, or a listing without the field — and the caller gets `fallback`
// instead. Silently going unbounded there would put the hole back exactly where
// an attacker who controls the response would want it, since omitting a header
// is entirely within their gift.
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
	// Room for exactly one byte beyond the limit, which is what distinguishes
	// "the stream was N bytes" from "the stream was longer than N". Refusing at
	// b.read == b.N instead would reject a download that is precisely its
	// declared size — the ordinary case, not an attack. That off-by-one was
	// written here first and the exactly-N test caught it.
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

// Counter counts the bytes read through it. Wrap the COMPRESSED side with one
// and hand it to Ratio.
type Counter struct {
	R io.Reader
	n int64
}

func (c *Counter) Read(p []byte) (int, error) {
	n, err := c.R.Read(p)
	c.n += int64(n)
	return n, err
}

// Count is the number of bytes read so far.
func (c *Counter) Count() int64 { return c.n }

// Ratio bounds a decompressed stream against the compressed bytes actually
// consumed to produce it, rather than against a size the sender declared.
//
// This is the stronger of the two bounds in this package and the one to prefer.
// A declared size — a Content-Length, a listing's compressed_size — is chosen
// by whoever is serving the response, so an attacker who can serve a bomb can
// also declare whatever size makes it fit. Bytes actually transferred cannot be
// faked: expanding to a gigabyte now costs 32 MB of real upload at the default
// ratio, which is the property that makes the attack uninteresting.
//
// It also needs no constant per call site. Limit and LimitExpansion require
// somebody to know how big a download ought to be, and that number goes stale
// silently as the data grows; this one does not care.
type Ratio struct {
	R     io.Reader // the decompressed side
	Src   *Counter  // the compressed side, already wrapped
	Max   int64     // output bytes permitted per input byte; 0 means MaxExpansion
	Floor int64     // always permit this much, whatever the ratio says
	What  string

	read int64
}

// LimitRatio wraps the decompressed side of src.
//
// Floor is what makes this usable at all: a decompressor reads its input in
// blocks and can legitimately emit a burst of output before it has consumed
// much, so the very start of any stream looks like an enormous ratio. Below the
// floor the ratio is not consulted. 1 MiB is far above any decompressor's
// look-ahead and far below anything worth defending against.
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
	// The floor RAISES the allowance; it does not gate the check. Written the
	// other way round first — `ratio applies only once Src.Count()*max exceeds
	// the floor` — and that is precisely backwards: a bomb's whole nature is a
	// tiny compressed side, so the allowance never reached the floor and the
	// ratio never applied. The test that asserts a bomb stops NEAR the floor
	// rather than merely stopping is what caught it; one that only checked for
	// an eventual error would have passed while 29 MB expanded.
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
