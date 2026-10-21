//go:build !devfeeds

// Command readdev serves development feeds, and only exists in a binary
// built with -tags devfeeds (docs/specs/api.md, Development-only). This
// file makes the package build without that tag, and says so.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "readdev is a development tool: build it with -tags devfeeds (`go run -tags devfeeds ./cmd/readdev`)")
	os.Exit(2)
}
