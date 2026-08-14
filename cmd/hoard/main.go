package main

import (
	"os"

	"github.com/spiffcs/hoard/internal/command"
)

func main() { os.Exit(command.Run(os.Args[1:])) }
