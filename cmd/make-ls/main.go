package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/owenrumney/go-lsp/lsp"
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
	srv := server.NewServer(h, server.WithPositionEncoding(lsp.PositionEncodingUTF8))
	return srv.Run(ctx, server.RunStdio())
}
