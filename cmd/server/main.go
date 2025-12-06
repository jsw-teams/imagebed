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

	log.Printf("[imagebed] loading config from %s", *cfgPath)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("[imagebed] failed to load config: %v", err)
	}

	if cfg.Installed {
		log.Printf("[imagebed] config loaded, status: INSTALLED (normal mode)")
	} else {
		log.Printf("[imagebed] config loaded, status: NOT INSTALLED (setup mode)")
	}

	srv, err := httpserver.NewServer(*cfgPath, cfg)
	if err != nil {
		log.Fatalf("[imagebed] failed to init http server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("[imagebed] HTTP server listening on %s", srv.Addr())
		if err := srv.Run(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[imagebed] http server error: %v", err)
		}
	}()

	// 阻塞直到收到 SIGINT / SIGTERM
	<-ctx.Done()
	log.Printf("[imagebed] shutdown signal received, stopping server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[imagebed] server shutdown error: %v", err)
	} else {
		log.Printf("[imagebed] server shutdown completed")
	}

	srv.CloseDB()
	log.Printf("[imagebed] database connections closed, exit now")
}