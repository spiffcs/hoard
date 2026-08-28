package schemagen

import (
	"testing"

	"github.com/spiffcs/hoard/internal/hoardjson"
)

func TestAVersionDocumentValidates(t *testing.T) {
	sch := compileSchema(t)

	doc := hoardjson.Document{
		SchemaVersion: hoardjson.SchemaVersion,
		Kind:          hoardjson.KindVersion,
		Version: &hoardjson.Version{
			Version:  "0.4.0",
			Commit:   "d7f76ab9c89cad064bbdfeaf8c32cdeec3ffd979",
			Built:    "2026-08-27T22:14:22Z",
			Go:       "go1.26.7",
			Platform: "darwin/arm64",
			Update: &hoardjson.Update{
				Latest: "v0.4.1",
				URL:    "https://github.com/spiffcs/hoard/releases/tag/v0.4.1",
			},
		},
	}
	if err := validate(t, sch, doc); err != nil {
		t.Errorf("a version document does not validate: %v", err)
	}
}

func TestAVersionDocumentWithoutAnUpdateValidates(t *testing.T) {
	sch := compileSchema(t)

	doc := hoardjson.Document{
		SchemaVersion: hoardjson.SchemaVersion,
		Kind:          hoardjson.KindVersion,
		Version: &hoardjson.Version{
			Version: "0.4.0", Commit: "d7f76ab", Built: "2026-08-27T22:14:22Z",
			Go: "go1.26.7", Platform: "darwin/arm64",
		},
	}
	if err := validate(t, sch, doc); err != nil {
		t.Errorf("a current build's version document does not validate: %v", err)
	}
}
