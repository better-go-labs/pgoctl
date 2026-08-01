package main

import (
	"fmt"
	"os"
)

const version = "0.0.1-wip"

func main() {
	// Cobra CLI with subcommands (collect/validate/merge/explain/compare) wired in D6.
	fmt.Fprintf(os.Stderr, "pgoctl %s — CLI coming in Week 2 (D6+)\n", version)
	os.Exit(0)
}
