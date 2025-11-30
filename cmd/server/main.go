package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jsw-teams/imagebed/internal/config"
	"github.com/jsw-teams/imagebed/internal/httpserver"
)

func main() {
	cfgPath := flag.String("config", os.Getenv("IMAGEBED_CONFIG"), "path to JSON config file")
	flag.Parse()

	if *cfgPath == "" {
		log.Fatal("config path is required via -config or IMAGEBED_CONFIG")
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	srv, err := httpserver.NewServer(*cfgPath, cfg)
	if err != nil {
		log.Fatalf("failed to init http server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.Run(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	log.Printf("imagebed HTTP server listening on %s", srv.Addr())

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	srv.CloseDB()
}
