package scryfall

import "testing"

func TestParseCardURL(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantSet    string
		wantNumber string
		wantErr    bool
	}{
		{
			name:       "canonical with slug",
			raw:        "https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre",
			wantSet:    "uma",
			wantNumber: "7",
		},
		{
			name:       "without slug",
			raw:        "https://scryfall.com/card/uma/7",
			wantSet:    "uma",
			wantNumber: "7",
		},
		{
			name:       "www host",
			raw:        "https://www.scryfall.com/card/neo/123/kaito",
			wantSet:    "neo",
			wantNumber: "123",
		},
		{
			name:       "letter in collector number",
			raw:        "https://scryfall.com/card/sld/1234a/some-card",
			wantSet:    "sld",
			wantNumber: "1234a",
		},
		{
			name:       "surrounding whitespace",
			raw:        "  https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre  ",
			wantSet:    "uma",
			wantNumber: "7",
		},
		{
			name:    "wrong host",
			raw:     "https://example.com/card/uma/7/ulamog",
			wantErr: true,
		},
		{
			name:    "not a card path",
			raw:     "https://scryfall.com/sets/uma",
			wantErr: true,
		},
		{
			name:    "missing number",
			raw:     "https://scryfall.com/card/uma",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, number, err := ParseCardURL(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got set=%q number=%q", set, number)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if set != tt.wantSet || number != tt.wantNumber {
				t.Fatalf("got set=%q number=%q, want set=%q number=%q", set, number, tt.wantSet, tt.wantNumber)
			}
		})
	}
}

func TestParsePrice(t *testing.T) {
	if p := parsePrice(""); p != nil {
		t.Errorf("empty string: want nil, got %v", *p)
	}
	if p := parsePrice("not-a-number"); p != nil {
		t.Errorf("garbage: want nil, got %v", *p)
	}
	p := parsePrice("3.49")
	if p == nil || *p != 3.49 {
		t.Errorf("valid price: want 3.49, got %v", p)
	}
}
