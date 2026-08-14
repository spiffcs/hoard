package link

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"testing/iotest"
)

type vectors struct {
	FrameHeaderSize int            `json:"frameHeaderSize"`
	MaxFramePayload int            `json:"maxFramePayload"`
	Kinds           map[string]int `json:"kinds"`
	PairingSalt     string         `json:"pairingSalt"`
	ServiceType     string         `json:"serviceType"`
	HelloJSON       string         `json:"helloJSON"`

	Encode []struct {
		Kind        int    `json:"kind"`
		KindName    string `json:"kindName"`
		PayloadLen  int    `json:"payloadLen"`
		PayloadHex  string `json:"payloadHex"`
		EncodedHex  string `json:"encodedHex"`
		EncodedFull bool   `json:"encodedFull"`
		EncodedLen  int    `json:"encodedLen"`
	} `json:"encode"`

	Stream struct {
		BytesHex string `json:"bytesHex"`
		Frames   []struct {
			Kind       int    `json:"kind"`
			PayloadHex string `json:"payloadHex"`
		} `json:"frames"`
	} `json:"stream"`

	DerivedKeys []struct {
		Digits  string `json:"digits"`
		KeyHex  string `json:"keyHex"`
		Display string `json:"display"`
	} `json:"derivedKeys"`

	Proofs []struct {
		Digits         string `json:"digits"`
		Session        string `json:"session"`
		FingerprintHex string `json:"fingerprintHex"`
		Proof          string `json:"proof"`
		Verifies       bool   `json:"verifies"`
	} `json:"proofs"`

	Negative struct {
		ProofBoundToPattern32 string `json:"proofBoundToPattern32"`
		VerifiesAgainstZeros  bool   `json:"verifiesAgainstZeros"`
		VerifiesAgainstNil    bool   `json:"verifiesAgainstNil"`
		VerifiesAgainstRight  bool   `json:"verifiesAgainstRight"`
	} `json:"negative"`
}

func load(t *testing.T) vectors {
	t.Helper()
	data, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	return v
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func TestConstants(t *testing.T) {
	v := load(t)
	if v.FrameHeaderSize != HeaderSize {
		t.Errorf("HeaderSize = %d, Swift says %d", HeaderSize, v.FrameHeaderSize)
	}
	if v.MaxFramePayload != MaxPayload {
		t.Errorf("MaxPayload = %d, Swift says %d", MaxPayload, v.MaxFramePayload)
	}
	if v.ServiceType != ServiceType {
		t.Errorf("ServiceType = %q, Swift says %q", ServiceType, v.ServiceType)
	}
	if v.PairingSalt != pairingSalt {
		t.Errorf("pairingSalt = %q, Swift says %q", pairingSalt, v.PairingSalt)
	}

	for name, want := range v.Kinds {
		var got Kind
		switch name {
		case "ndjson":
			got = KindNDJSON
		case "preview":
			got = KindPreview
		case "still":
			got = KindStill
		case "trace":
			got = KindTrace
		default:
			t.Errorf("Swift has kind %q that Go does not know", name)
			continue
		}
		if int(got) != want {
			t.Errorf("kind %s = %d, Swift says %d", name, got, want)
		}
		if got.String() != name {
			t.Errorf("Kind(%d).String() = %q, Swift calls it %q", got, got.String(), name)
		}
	}
	if len(v.Kinds) != 4 {
		t.Errorf("Swift has %d kinds, this test knows 4", len(v.Kinds))
	}
}

func TestEncodeMatchesSwift(t *testing.T) {
	v := load(t)
	if len(v.Encode) == 0 {
		t.Fatal("no encode vectors")
	}
	for _, c := range v.Encode {
		got, err := Encode(Frame{Kind: Kind(c.Kind), Payload: unhex(t, c.PayloadHex)})
		if err != nil {
			t.Errorf("%s/%d: Encode: %v", c.KindName, c.PayloadLen, err)
			continue
		}
		if len(got) != c.EncodedLen {
			t.Errorf("%s/%d: encoded %d bytes, Swift made %d",
				c.KindName, c.PayloadLen, len(got), c.EncodedLen)
			continue
		}
		want := unhex(t, c.EncodedHex)
		if c.EncodedFull {
			if !bytes.Equal(got, want) {
				t.Errorf("%s/%d:\n got %x\nwant %x", c.KindName, c.PayloadLen, got, want)
			}
			continue
		}

		if !bytes.Equal(got[:HeaderSize], want) {
			t.Errorf("%s/%d header:\n got %x\nwant %x",
				c.KindName, c.PayloadLen, got[:HeaderSize], want)
		}
	}
}

func TestWriteFrameMatchesEncode(t *testing.T) {
	v := load(t)
	for _, c := range v.Encode {
		f := Frame{Kind: Kind(c.Kind), Payload: unhex(t, c.PayloadHex)}
		want, err := Encode(f)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		var buf bytes.Buffer
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
		if !bytes.Equal(buf.Bytes(), want) {
			t.Errorf("%s/%d: WriteFrame disagrees with Encode", c.KindName, c.PayloadLen)
		}
	}
}

func TestReadStreamMatchesSwift(t *testing.T) {
	v := load(t)
	raw := unhex(t, v.Stream.BytesHex)
	if len(v.Stream.Frames) == 0 {
		t.Fatal("no stream vectors")
	}

	readers := map[string]func() io.Reader{
		"whole":   func() io.Reader { return bytes.NewReader(raw) },
		"oneByte": func() io.Reader { return iotest.OneByteReader(bytes.NewReader(raw)) },
		"half":    func() io.Reader { return iotest.HalfReader(bytes.NewReader(raw)) },
		"dataErr": func() io.Reader { return iotest.DataErrReader(bytes.NewReader(raw)) },
	}

	for name, mk := range readers {
		t.Run(name, func(t *testing.T) {
			r := mk()
			for i, want := range v.Stream.Frames {
				got, err := ReadFrame(r, MaxPayload)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if int(got.Kind) != want.Kind {
					t.Errorf("frame %d: kind %d, want %d", i, got.Kind, want.Kind)
				}
				wantPayload := unhex(t, want.PayloadHex)
				if !bytes.Equal(got.Payload, wantPayload) {
					t.Errorf("frame %d: payload %x, want %x", i, got.Payload, wantPayload)
				}
			}

			if _, err := ReadFrame(r, MaxPayload); !errors.Is(err, io.EOF) {
				t.Errorf("after last frame: err = %v, want io.EOF", err)
			}
		})
	}
}

func TestDerivedKeyMatchesSwift(t *testing.T) {
	v := load(t)
	if len(v.DerivedKeys) == 0 {
		t.Fatal("no key vectors")
	}
	for _, c := range v.DerivedKeys {
		code, err := ParseCode(c.Digits)
		if err != nil {
			t.Errorf("ParseCode(%q): %v", c.Digits, err)
			continue
		}
		key, err := code.key()
		if err != nil {
			t.Errorf("key(%q): %v", c.Digits, err)
			continue
		}
		if got := hex.EncodeToString(key); got != c.KeyHex {
			t.Errorf("key(%q):\n got %s\nwant %s", c.Digits, got, c.KeyHex)
		}
		if got := code.Display(); got != c.Display {
			t.Errorf("Display(%q) = %q, Swift says %q", c.Digits, got, c.Display)
		}
	}
}

func TestProofMatchesSwift(t *testing.T) {
	v := load(t)
	if len(v.Proofs) == 0 {
		t.Fatal("no proof vectors")
	}
	for _, c := range v.Proofs {
		code, err := ParseCode(c.Digits)
		if err != nil {
			t.Fatalf("ParseCode(%q): %v", c.Digits, err)
		}
		var fp []byte
		if c.FingerprintHex != "" {
			fp = unhex(t, c.FingerprintHex)
		}
		got, err := Proof(c.Session, code, fp)
		if err != nil {
			t.Errorf("Proof: %v", err)
			continue
		}
		if got != c.Proof {
			t.Errorf("Proof(%q, %q, %s):\n got %s\nwant %s",
				c.Session, c.Digits, c.FingerprintHex, got, c.Proof)
		}

		if !c.Verifies {
			t.Fatalf("vector claims Swift could not verify its own proof")
		}
		if !VerifyProof(c.Proof, c.Session, code, fp) {
			t.Errorf("VerifyProof rejected a proof Swift accepted (%q)", c.Session)
		}
	}
}

func TestProofBindingRejects(t *testing.T) {
	v := load(t)
	code, err := ParseCode("123456")
	if err != nil {
		t.Fatal(err)
	}
	pattern32 := make([]byte, 32)
	for i := range pattern32 {
		pattern32[i] = byte((i*31 + 7) & 0xFF)
	}
	zeros := make([]byte, 32)

	bound := v.Negative.ProofBoundToPattern32
	if got, err := Proof("s", code, pattern32); err != nil || got != bound {
		t.Fatalf("Proof bound to pattern32:\n got %s (err %v)\nwant %s", got, err, bound)
	}
	if v.Negative.VerifiesAgainstZeros || v.Negative.VerifiesAgainstNil {
		t.Fatal("vector says Swift accepts a mismatched binding; the wire contract is broken")
	}
	if VerifyProof(bound, "s", code, zeros) {
		t.Error("accepted a proof bound to a different certificate (zeros)")
	}
	if VerifyProof(bound, "s", code, nil) {
		t.Error("accepted a fingerprint-bound proof as unbound")
	}
	if !VerifyProof(bound, "s", code, pattern32) {
		t.Error("rejected a correctly bound proof")
	}

	if VerifyProof(bound, "s2", code, pattern32) {
		t.Error("accepted a proof replayed onto a different session")
	}

	other, err := ParseCode("045208")
	if err != nil {
		t.Fatal(err)
	}
	if VerifyProof(bound, "s", other, pattern32) {
		t.Error("accepted a proof made with a different pairing code")
	}
}

func TestHelloJSONMatchesSwift(t *testing.T) {
	v := load(t)
	h := Hello{
		Role:    RoleControl,
		Session: "1E1D6C1A-0000-4000-8000-000000000001",
		Proof:   "cHJvb2Y=",
	}
	got, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}

	var gotObj, wantObj map[string]any
	if err := json.Unmarshal(got, &gotObj); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(v.HelloJSON), &wantObj); err != nil {
		t.Fatal(err)
	}
	if len(gotObj) != len(wantObj) {
		t.Errorf("hello has %d keys, Swift has %d:\n got %s\nwant %s",
			len(gotObj), len(wantObj), got, v.HelloJSON)
	}
	for k, want := range wantObj {
		gotV, ok := gotObj[k]
		if !ok {
			t.Errorf("hello is missing key %q that Swift emits", k)
			continue
		}
		if gotV != want {
			t.Errorf("hello[%q] = %v, Swift says %v", k, gotV, want)
		}
	}

	if !strings.Contains(string(got), `"name"`) {
		t.Errorf("hello dropped the empty name key: %s", got)
	}
}
