package link

import (
	"errors"
	"testing"
)

func TestParseCode(t *testing.T) {

	ok := map[string]string{
		"045208":   "045208",
		"045 208":  "045208",
		"045-208":  "045208",
		" 045208 ": "045208",
		"0 4 5208": "045208",
		"000000":   "000000",
	}
	for in, want := range ok {
		c, err := ParseCode(in)
		if err != nil {
			t.Errorf("ParseCode(%q): %v", in, err)
			continue
		}
		if c.Digits() != want {
			t.Errorf("ParseCode(%q) = %q, want %q", in, c.Digits(), want)
		}
	}

	bad := []string{"", "12345", "1234567", "abcdef", "12 34", "045208x9"}
	for _, in := range bad {
		if _, err := ParseCode(in); !errors.Is(err, ErrBadCode) {
			t.Errorf("ParseCode(%q): err = %v, want ErrBadCode", in, err)
		}
	}
}

func TestCodeZero(t *testing.T) {
	var zero Code
	if !zero.IsZero() {
		t.Error("the zero Code does not report itself as zero")
	}
	c, err := ParseCode("123456")
	if err != nil {
		t.Fatal(err)
	}
	if c.IsZero() {
		t.Error("a parsed code reports itself as zero")
	}

	if _, err := Proof("s", zero, nil); err != nil {
		t.Errorf("Proof with the zero code errored rather than producing a value: %v", err)
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	c, err := ParseCode("123456")
	if err != nil {
		t.Fatal(err)
	}
	good, err := Proof("session", c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyProof(good, "session", c, nil) {
		t.Fatal("a freshly made proof did not verify")
	}

	junk := []string{
		"", "x", "!!!!not base64!!!!",
		good[:len(good)-1],
		good + "=",
		"A" + good[1:],
		good[:len(good)-2] + "AA",
	}
	for _, j := range junk {
		if VerifyProof(j, "session", c, nil) {
			t.Errorf("VerifyProof accepted %q", j)
		}
	}
}

func TestEmptyVersusNilFingerprint(t *testing.T) {
	c, err := ParseCode("123456")
	if err != nil {
		t.Fatal(err)
	}
	unbound, err := Proof("s", c, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := Proof("s", c, []byte{})
	if err != nil {
		t.Fatal(err)
	}
	if unbound == empty {
		t.Error("a nil fingerprint and an empty one produce the same proof; " +
			"the 0x00 separator is not being written")
	}
}

func TestSessionFreshness(t *testing.T) {
	c, err := ParseCode("123456")
	if err != nil {
		t.Fatal(err)
	}
	a, err := Proof("session-a", c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if VerifyProof(a, "session-b", c, nil) {
		t.Error("a proof for one session verified against another")
	}
}

func TestDisplay(t *testing.T) {
	c, err := ParseCode("045208")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Display(); got != "045 208" {
		t.Errorf("Display() = %q, want %q", got, "045 208")
	}

	var zero Code
	if got := zero.Display(); got != "" {
		t.Errorf("zero Display() = %q, want empty", got)
	}
}
