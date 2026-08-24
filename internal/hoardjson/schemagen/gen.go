// Package schemagen derives the JSON Schema under schema/json/ from the
// hoardjson document model, so the schema cannot drift from what the code
// emits: the structs are the source of truth, the generator writes the files,
// and a test regenerates and compares.
//
// It is a separate package from hoardjson so the hoard binary, which imports
// hoardjson on every JSON command, never links the reflection machinery.
package schemagen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/invopop/jsonschema"

	"github.com/spiffcs/hoard/internal/hoardjson"
)

// SchemaID is the released schema's identity, versioned so a document's
// schemaVersion names exactly one immutable file.
func SchemaID() string {
	return fmt.Sprintf(
		"https://raw.githubusercontent.com/spiffcs/hoard/main/schema/json/schema-%s.json",
		hoardjson.SchemaVersion)
}

// RepoRoot locates the repository root from this file's compiled position, so
// the generator and the drift test resolve the same paths whatever their
// working directory.
func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// Generate reflects the document model into schema bytes: draft 2020-12,
// two-space indented, doc comments carried over as descriptions. Fields
// without omitempty are required; documents carry nothing undeclared
// (additionalProperties is false), so a typo'd consumer assumption fails
// validation instead of silently reading nulls.
func Generate() ([]byte, error) {
	r := &jsonschema.Reflector{ExpandedStruct: true}
	// AddGoComments keys each comment as base joined with the path it walked,
	// verbatim — so the walk must start at "./" with the module root as the
	// working directory, or no key matches an import path and every
	// description silently vanishes. The generator and the drift test run
	// from different directories, hence the chdir.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(RepoRoot()); err != nil {
		return nil, err
	}
	commentsErr := r.AddGoComments("github.com/spiffcs/hoard", "./")
	if err := os.Chdir(cwd); err != nil {
		return nil, err
	}
	if commentsErr != nil {
		return nil, fmt.Errorf("reading model comments: %w", commentsErr)
	}
	schema := r.Reflect(&hoardjson.Document{})
	schema.ID = jsonschema.ID(SchemaID())

	out, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
