package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

type Report struct {
	Out, Err       io.Writer
	OutEnv, ErrEnv Env
}

func NewReport() *Report {
	return &Report{
		Out: os.Stdout, Err: os.Stderr,
		OutEnv: Detect(os.Stdout), ErrEnv: Detect(os.Stderr),
	}
}

func (r *Report) Success(format string, a ...any) {
	fmt.Fprintln(r.Out, r.OutEnv.OK()("✓ "+fmt.Sprintf(format, a...)))
}

func (r *Report) Result(format string, a ...any) {
	fmt.Fprintf(r.Out, format+"\n", a...)
}

func (r *Report) Detail(format string, a ...any) {
	fmt.Fprintf(r.Out, "  "+format+"\n", a...)
}

func (r *Report) Item(s string) {
	fmt.Fprintln(r.Out, "    - "+s)
}

func (r *Report) Hint(format string, a ...any) {
	fmt.Fprintln(r.Out, r.OutEnv.Dim()(fmt.Sprintf(format, a...)))
}

func (r *Report) Warn(format string, a ...any) {
	fmt.Fprintln(r.Err, r.ErrEnv.Warn()("  ! "+fmt.Sprintf(format, a...)))
}

func (r *Report) Progress(format string, a ...any) {
	fmt.Fprintln(r.Err, r.ErrEnv.Dim()(fmt.Sprintf(format, a...)))
}

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
