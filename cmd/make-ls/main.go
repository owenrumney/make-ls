package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/owenrumney/go-lsp/server"
	"github.com/owenrumney/make-ls/internal/handler"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	h := handler.New()
	srv := server.NewServer(h)
	return srv.Run(ctx, server.RunStdio())
}
