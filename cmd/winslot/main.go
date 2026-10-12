// Command winslot manages licensed Windows VM slots on a libvirt host.
// See README.md for the design; `winslot --help` for usage.
package main

import (
	"os"

	"winslot/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
