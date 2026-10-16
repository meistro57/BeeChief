// Command beechief is BeeChief's entrypoint: a terminal operations console
// for OpenClaw agents across multiple machines.
package main

import (
	"fmt"
	"os"

	"github.com/meistro57/beechief/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "beechief: %v\n", err)
		os.Exit(1)
	}
}
