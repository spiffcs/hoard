package ui

// Report is the prose dialect for mutating commands — one vocabulary for
// "what just happened", grounded in the patterns the commands already
// speak: a headline, two-space details, four-space bullets, dim hints.
// Tables have the Table engine; this is everything around them.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Report writes a command's account of itself. The stream policy is
// clig.dev's: the command's outcome goes to Out (stdout — the pipeable
// record), narration about work in progress and partial-outcome warnings
// go to Err (stderr — conversation with the user). Each stream carries its
// own Env because stdout piped with stderr still a terminal is the common
// shape of `hoard import x.csv > log`.
type Report struct {
	Out, Err       io.Writer
	OutEnv, ErrEnv Env
}

// NewReport detects both real streams. Tests build the struct directly
// with buffers and pinned Envs instead.
func NewReport() *Report {
	return &Report{
		Out: os.Stdout, Err: os.Stderr,
		OutEnv: Detect(os.Stdout), ErrEnv: Detect(os.Stderr),
	}
}

// Success is a mutation confirmation: "✓ Added …". The glyph is emitted
// always — it joins the ×/—/* vocabulary and survives piping — while the
// color is TTY-only.
func (r *Report) Success(format string, a ...any) {
	fmt.Fprintln(r.Out, r.OutEnv.OK()("✓ "+fmt.Sprintf(format, a...)))
}

// Result is a plain outcome headline.
func (r *Report) Result(format string, a ...any) {
	fmt.Fprintf(r.Out, format+"\n", a...)
}

// Detail is a two-space-indented line under a headline.
func (r *Report) Detail(format string, a ...any) {
	fmt.Fprintf(r.Out, "  "+format+"\n", a...)
}

// Item is one four-space bullet in a list a Detail introduced.
func (r *Report) Item(s string) {
	fmt.Fprintln(r.Out, "    - "+s)
}

// Hint is dim advisory prose — provenance, what to try next. It stays on
// Out: the unpriced report's advice already lives there, and a hint is
// still part of the command's answer.
func (r *Report) Hint(format string, a ...any) {
	fmt.Fprintln(r.Out, r.OutEnv.Dim()(fmt.Sprintf(format, a...)))
}

// Warn is a partial-outcome warning: rows skipped, data missing. It goes
// to Err — a pipe reading the outcome should not have to filter caveats —
// with a "! " marker so the piped form still reads as a warning.
func (r *Report) Warn(format string, a ...any) {
	fmt.Fprintln(r.Err, r.ErrEnv.Warn()("  ! "+fmt.Sprintf(format, a...)))
}

// Progress is dim narration about work being done, on Err with the rest
// of the narration (ui.Printer owns the event-stream form; this is the
// one-off line).
func (r *Report) Progress(format string, a ...any) {
	fmt.Fprintln(r.Err, r.ErrEnv.Dim()(fmt.Sprintf(format, a...)))
}

// Confirm asks a yes/no question on out and reads one line from in;
// anything but an explicit yes — including EOF — declines. The questions
// this asks all precede spending somebody's bandwidth or doubling their
// cards, and the safe reading of a stray keystroke is "don't".
func Confirm(in io.Reader, out io.Writer, question string) (bool, error) {
	fmt.Fprintf(out, "%s [y/N] ", question)
	sc := bufio.NewScanner(in)
	if !sc.Scan() {
		return false, sc.Err()
	}
	switch strings.ToLower(strings.TrimSpace(sc.Text())) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
