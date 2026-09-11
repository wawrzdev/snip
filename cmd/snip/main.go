package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wawrzdev/snip/internal/snip"
)

var version = "dev"

func main() {
	app := snip.NewApp(os.Stdin, os.Stdout, os.Stderr)
	app.Version = version
	if err := app.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "snip: %v\n", err)
		os.Exit(1)
	}
}
