package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Xmandon/xdom/internal/xpay"
)

func main() {
	cfg := xpay.LoadConfigFromEnv()
	application, err := xpay.New(cfg)
	if err != nil {
		xpay.LogStartupError(err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := application.Run(ctx); err != nil {
		xpay.LogStartupError(err)
		os.Exit(1)
	}
}
