package demo

import _ "embed"

//go:embed collection.json
var Collection []byte

//go:embed history.json
var History []byte
