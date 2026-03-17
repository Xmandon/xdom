package main

import (
	"log"
	"os"

	"github.com/Xmandon/xdom/internal/app"
)

func main() {
	cfg := app.LoadConfigFromEnv()
	server := app.NewServer(cfg)
	log.Printf("starting %s on %s", cfg.ServiceName, cfg.ListenAddr)
	if err := server.ListenAndServe(); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
