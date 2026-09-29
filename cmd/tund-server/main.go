// Command tund-server is the tund edge: it terminates TLS, accepts tund
// clients and routes public hostnames through their tunnels.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"tund/internal/server"
)

func main() {
	log.SetFlags(log.LstdFlags) // local time (set TZ), like the certificate logs
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("tund-server", server.Version)
		return
	}
	cfg, err := server.LoadConfig()
	if err != nil {
		log.Fatalf("configuration error:\n%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := server.OpenStore(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	srv, err := server.New(cfg, store)
	if err != nil {
		log.Fatal(err)
	}
	if err := srv.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
