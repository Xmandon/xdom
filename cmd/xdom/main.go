package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Xmandon/xdom/internal/app"
)

func main() {
	cfg := app.LoadConfigFromEnv()
	application, err := app.New(cfg)
	if err != nil {
		app.LogStartupError(err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := application.Run(ctx); err != nil {
		app.LogStartupError(err)
		os.Exit(1)
	}
}
