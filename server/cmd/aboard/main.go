// Command aboard is the Aboard command-line client. It also runs the local server in
// the background when a command needs one.
package main

import (
	"context"
	"os"

	"github.com/leonidas1712/aboard/server/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], cli.OSEnv()))
}
