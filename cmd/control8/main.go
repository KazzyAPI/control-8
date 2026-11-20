package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/KazzyAPI/control-8/internal/api"
	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/config"
	rt "github.com/KazzyAPI/control-8/internal/runtime"
	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
	"github.com/KazzyAPI/control-8/internal/store"
	"github.com/KazzyAPI/control-8/internal/targets"
)

func main() {
	if len(os.Args) > 1 {
		os.Exit(subcommand(os.Args[1]))
	}
	cfg, err := config.FromOS()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var lvl slog.Level
	lvl.UnmarshalText([]byte(cfg.LogLevel))
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))

	tokens, err := auth.ParseTokens(cfg.Tokens, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "control8.db"))
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	specs, err := targets.Parse(cfg.Targets)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := targets.Sync(db, specs); errors.Is(err, targets.ErrInUse) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	} else if err != nil {
		log.Error("target sync", "err", err)
		os.Exit(1)
	}
	dyn := targets.Dynamic(specs, os.Getenv, os.ReadFile)
	drivers := func(name string) (rt.Driver, error) {
		c, err := dyn(name)
		if err != nil {
			return nil, err
		}
		return k8s.NewDriver(c), nil
	}
	tc := targets.NewChecker(specs, targets.BuildClient(os.Getenv, os.ReadFile))

	if err := auth.SyncTokens(db, tokens, time.Now(), log); err != nil {
		log.Error("token sync", "err", err)
		os.Exit(1)
	}

	if err := auth.Bootstrap(db, cfg.BootstrapAdminGrp); err != nil {
		log.Error("bootstrap admin", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	tc.Run(ctx, cfg.TargetCheck)
	// BaseContext ends open SSE streams on shutdown (EVT-015)
	srv := &http.Server{Addr: cfg.Listen, Handler: api.New(db, log, cfg, tc, drivers), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
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

// subcommand runs hash-password (AUTH-015) and gen-token (AUTH-030); neither touches config or the DB.
func subcommand(name string) int {
	switch name {
	case "hash-password":
		pw, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if pw = strings.TrimRight(pw, "\r\n"); pw == "" {
			fmt.Fprintln(os.Stderr, "hash-password: empty password on stdin", err)
			return 2
		}
		fmt.Println(auth.HashPassword(pw))
	case "gen-token":
		plain, hash := auth.GenToken()
		fmt.Printf("token:  %s\nsha256: %s\n", plain, hash)
	default:
		fmt.Fprintf(os.Stderr, "usage: control8 [hash-password|gen-token]\n")
		return 2
	}
	return 0
}
