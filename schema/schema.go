// Package schema carries hoard's released JSON Schema into the binary.
//
// It sits beside the generated files rather than under internal/ because an
// embed pattern may not contain "..": only a package above the files in the
// tree can embed them, and the files live here by contract — the $id in
// every document hoard emits points a consumer at schema/json/schema-X.Y.Z.json
// in this repository. The alternative was a second copy under internal/, which
// is precisely the drift the generator and its test exist to prevent.
//
// Nothing derives the schema at runtime. internal/hoardjson/schemagen reflects
// the document model with AddGoComments, which reads the Go source from the
// repository root, so it can answer for the generator and the drift test and
// never for an installed binary.
package schema

import _ "embed"

// Latest is schema/json/schema-latest.json verbatim: the schema for the
// document version this build emits.
//
// Verbatim matters. schemagen's drift test holds that file byte-for-byte to
// what the model generates, so these bytes describe the JSON this binary
// actually writes — and a consumer can diff what `hoard schema` printed
// against the published file the $id names and get no difference at all.
//
//go:embed json/schema-latest.json
var Latest []byte
