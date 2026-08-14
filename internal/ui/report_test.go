package ui

import (
	"strings"
	"testing"
)

func TestReportPipedBytes(t *testing.T) {
	var out, errBuf strings.Builder
	r := &Report{Out: &out, Err: &errBuf}

	r.Success("Added 2× Sol Ring")
	r.Result("Imported %d cards.", 3)
	r.Detail("%d into %s", 2, "Binder")
	r.Item("No Such Card")
	r.Hint("Try: hoard movers")
	r.Warn("%d rows skipped", 4)
	r.Progress("fetching prices")

	wantOut := "✓ Added 2× Sol Ring\n" +
		"Imported 3 cards.\n" +
		"  2 into Binder\n" +
		"    - No Such Card\n" +
		"Try: hoard movers\n"
	if out.String() != wantOut {
		t.Errorf("stdout:\n%q\nwant:\n%q", out.String(), wantOut)
	}
	wantErr := "  ! 4 rows skipped\n" +
		"fetching prices\n"
	if errBuf.String() != wantErr {
		t.Errorf("stderr:\n%q\nwant:\n%q", errBuf.String(), wantErr)
	}
	for name, s := range map[string]string{"stdout": out.String(), "stderr": errBuf.String()} {
		if strings.Contains(s, "\x1b") {
			t.Errorf("%s carries escapes without color: %q", name, s)
		}
	}
}

func TestReportStyledCarriesSGR(t *testing.T) {
	var out, errBuf strings.Builder
	r := &Report{Out: &out, Err: &errBuf,
		OutEnv: Env{Color: true}, ErrEnv: Env{Color: true}}

	r.Success("done")
	r.Hint("try this")
	r.Warn("skipped")
	r.Progress("working")

	if !strings.Contains(out.String(), "\x1b[") {
		t.Errorf("styled stdout has no escapes: %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "\x1b[") {
		t.Errorf("styled stderr has no escapes: %q", errBuf.String())
	}

	out.Reset()
	r.Result("Imported 3 cards.")
	r.Detail("2 into Binder")
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("Result/Detail must never style: %q", out.String())
	}
}

func TestConfirm(t *testing.T) {
	for input, want := range map[string]bool{
		"y\n": true, "Y\n": true, "yes\n": true, "YES\n": true,
		"n\n": false, "no\n": false, "\n": false, "sure\n": false, "": false,
	} {
		var prompt strings.Builder
		got, err := Confirm(strings.NewReader(input), &prompt, "Download it?")
		if err != nil {
			t.Errorf("Confirm(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("Confirm(%q) = %v, want %v", input, got, want)
		}
		if prompt.String() != "Download it? [y/N] " {
			t.Errorf("prompt = %q", prompt.String())
		}
	}
}
