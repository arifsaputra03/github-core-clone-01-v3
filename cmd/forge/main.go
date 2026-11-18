// Command forge is the git forge server: one process, no outside service.
// It listens on an ephemeral port (or FORGE_PORT), prints FORGE_PORT=<port>
// on stdout for main.ts, and runs until it is killed. The embedded
// file-based store is the default; set DATABASE_URL (and build with
// -tags postgres) to use PostgreSQL instead.
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"forge/internal/config"
	"forge/internal/http"
	"forge/internal/service"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	cfg := config.Load()
	st, err := openStore(cfg)
	if err != nil {
		log.Fatalf("forge: open store: %v", err)
	}
	defer st.Close()
	svc := service.New(st, cfg.DataDir, cfg.ViewerLogin)
	srv := http.New(cfg, svc)
	port, err := srv.Listen()
	if err != nil {
		log.Fatalf("forge: listen: %v", err)
	}
	// main.ts reads this line to learn the port.
	fmt.Printf("FORGE_PORT=%d\n", port)
	log.Printf("forge: listening on 127.0.0.1:%d (data dir %s, viewer %s)", port, cfg.DataDir, cfg.ViewerLogin)
	// main.ts kills the process when the trial is done; wait for that.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("forge: shutting down")
}
