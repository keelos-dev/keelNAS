// keelnasd is keelNAS's management daemon and, invoked as keelnas (or keelnasd COMMAND),
// its command line. docs/04-keelnasd.md is the specification.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"keelnas/internal/cli"
	"keelnas/internal/nas"
	"keelnas/internal/sys"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if filepath.Base(os.Args[0]) != "keelnas" {
		switch {
		case len(args) > 0 && args[0] == "serve":
			serve()
			return
		case len(args) > 0 && args[0] == "version":
			fmt.Println("keelnasd", version)
			return
		case len(args) == 0:
			fmt.Fprintln(os.Stderr, "usage: keelnasd serve | version | COMMAND (see keelnas help)")
			os.Exit(2)
		}
	}
	os.Exit(cli.New(nas.Socket).Run(args))
}

func serve() {
	lg := log.New(os.Stdout, "", log.LstdFlags)
	srv, err := nas.NewServer(sys.Real{}, nas.StatePath, version, lg)
	if err != nil {
		lg.Fatalf("keelnasd: %v", err)
	}
	lg.Fatalf("keelnasd: %v", srv.Serve(nas.Socket))
}
