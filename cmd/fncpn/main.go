package main

import (
	"context"
	"os"

	"github.com/rectcircle/fn-connect-private-network/internal/command"
)

var version = "dev"

func main() {
	ctx, cancel := command.SignalContext(context.Background())
	defer cancel()
	os.Exit(command.Run(ctx, os.Args[1:], command.Environment{
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version,
	}))
}
