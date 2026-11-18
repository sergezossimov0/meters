// Command gateway simulates a LPWAN gateway: it models meters, buffers their
// readings on disk and sends them, together with heartbeats, to the server.
package main

import (
	"fmt"
	"os"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fmt.Fprintf(os.Stderr, "meters gateway %s: not implemented yet\n", version)
}
