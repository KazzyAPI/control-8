package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/KazzyAPI/control-8/internal/api"
	"github.com/KazzyAPI/control-8/internal/config"
	"github.com/KazzyAPI/control-8/internal/store"
)

func main() {
	cfg, err := config.FromOS()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var lvl slog.Level
	lvl.UnmarshalText([]byte(cfg.LogLevel))
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))

	db, err := store.Open(filepath.Join(cfg.DataDir, "control8.db"))
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	srv := &http.Server{Addr: cfg.Listen, Handler: api.New(db, log), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Info("listening", "addr", cfg.Listen)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}
