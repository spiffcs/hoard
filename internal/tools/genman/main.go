package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra/doc"

	"github.com/spiffcs/hoard/internal/command"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: genman <output-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]

	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "genman:", err)
		os.Exit(1)
	}

	header := &doc.GenManHeader{
		Title:   "HOARD",
		Section: "1",
		Source:  "hoard",
		Manual:  "hoard Manual",
	}

	if err := doc.GenManTree(command.DocTree(), header, dir); err != nil {
		fmt.Fprintln(os.Stderr, "genman:", err)
		os.Exit(1)
	}
}
