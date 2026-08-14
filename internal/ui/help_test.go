package ui

import (
	"slices"
	"strings"
	"testing"
)

func TestWrapHelpBreaksBetweenEntries(t *testing.T) {
	line := "enter accept · esc cancel · tab next"
	if got := WrapHelp(line, 100); !slices.Equal(got, []string{line}) {
		t.Fatalf("wide: %q, want one line", got)
	}
	got := WrapHelp(line, 25)
	want := []string{"enter accept · esc cancel", "tab next"}
	if !slices.Equal(got, want) {
		t.Fatalf("narrow: %q, want %q", got, want)
	}
	for _, l := range got {
		if strings.HasPrefix(l, "·") || strings.HasSuffix(l, "·") {
			t.Fatalf("line %q keeps a dangling separator", l)
		}
	}
}

func TestWrapHelpHonorsForcedBreaks(t *testing.T) {
	got := WrapHelp("archidekt links work\nMoxfield blocks fetching · enter accept", 500)
	want := []string{"archidekt links work", "Moxfield blocks fetching · enter accept"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWrapHelpWrapsWithinForcedSegments(t *testing.T) {
	got := WrapHelp("one · two\nthree · four · five", 12)
	want := []string{"one · two", "three · four", "five"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWrapHelpUnknownWidthKeepsForcedBreaks(t *testing.T) {
	got := WrapHelp("first · row\nsecond row", 0)
	want := []string{"first · row", "second row"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
