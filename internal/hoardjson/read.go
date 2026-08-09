package hoardjson

// Reading documents back. Every other file here emits; this one is the only
// consumer, and it exists because `hoard merge` moves a hoard between two
// databases as a document rather than by splicing SQLite files.
//
// The round trip is the point: merge writes a document and reads it back
// before applying it, so the model stays the contract between two hoards
// instead of a formatting detail on the way out.

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Read decodes one document, refusing what this build cannot faithfully
// interpret.
//
// Compatibility is judged on the MODEL component alone, per SchemaVer and
// schema/json/README.md: a higher MODEL means a field this build reads was
// renamed, removed or given a new meaning, so the document would be
// misunderstood rather than merely incomplete. A higher REVISION or ADDITION
// is accepted — the fields this build knows still mean what they meant, and
// the ones it does not know are precisely the ones it can ignore.
func Read(r io.Reader) (Document, error) {
	var doc Document
	dec := json.NewDecoder(r)
	if err := dec.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("reading hoard document: %w", err)
	}
	if doc.SchemaVersion == "" {
		return Document{}, fmt.Errorf("not a hoard document: no schemaVersion")
	}
	got, err := modelOf(doc.SchemaVersion)
	if err != nil {
		return Document{}, err
	}
	want, err := modelOf(SchemaVersion)
	if err != nil {
		return Document{}, err
	}
	if got > want {
		return Document{}, fmt.Errorf(
			"document is schema %s but this hoard understands %s; upgrade hoard to read it",
			doc.SchemaVersion, SchemaVersion)
	}
	return doc, nil
}

// ReadHoard decodes a hoard document specifically, so callers that need one
// do not repeat the kind check.
func ReadHoard(r io.Reader) (*Hoard, error) {
	doc, err := Read(r)
	if err != nil {
		return nil, err
	}
	if doc.Kind != KindHoard {
		return nil, fmt.Errorf("document is a %q, not a %q", doc.Kind, KindHoard)
	}
	if doc.Hoard == nil {
		return nil, fmt.Errorf("document says kind %q but carries no hoard payload", KindHoard)
	}
	return doc.Hoard, nil
}

// modelOf pulls the MODEL component out of a MODEL.REVISION.ADDITION version.
func modelOf(v string) (int, error) {
	model, _, ok := strings.Cut(v, ".")
	if !ok {
		return 0, fmt.Errorf("malformed schemaVersion %q (want MODEL.REVISION.ADDITION)", v)
	}
	n, err := strconv.Atoi(model)
	if err != nil {
		return 0, fmt.Errorf("malformed schemaVersion %q: %w", v, err)
	}
	return n, nil
}
