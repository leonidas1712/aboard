// Command aboard is the Aboard command-line client. It also runs the local server in
// the background when a command needs one.
package main

import (
	"context"
	"os"

	"github.com/leonidas1712/aboard/server/internal/cli"
)

func main() {
	// A file that can't be read leaves the system's roots alone; a server whose
	// certificate needed it then fails its TLS check, which says so. Hooks and every
	// other command keep working.
	_ = cli.TrustCertFile(os.Getenv)
	os.Exit(cli.Run(context.Background(), os.Args[1:], cli.OSEnv()))
}
