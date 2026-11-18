// Command server accepts meter readings from gateways, stores them in
// PostgreSQL and serves the customer/admin API and the status page.
package main

import (
	"fmt"
	"os"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fmt.Fprintf(os.Stderr, "meters server %s: not implemented yet\n", version)
}
