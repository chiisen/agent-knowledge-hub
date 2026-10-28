package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chiisen/agent-knowledge-hub/internal/config"
	"github.com/chiisen/agent-knowledge-hub/internal/httpapi"
	"github.com/chiisen/agent-knowledge-hub/internal/knowledge"
	"github.com/chiisen/agent-knowledge-hub/internal/sqlite"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := execute(logger); err != nil {
		logger.Error("knowledge-hub stopped", "error", err)
		os.Exit(1)
	}
}

func execute(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, os.Args[1:], logger)
}

func run(ctx context.Context, args []string, logger *slog.Logger) (err error) {
	cfg, err := config.Parse(args)
	if err != nil {
		return err
	}
	startupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := sqlite.Open(startupCtx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			logger.Error("close database", "error", closeErr)
			if err == nil {
				err = fmt.Errorf("close database: %w", closeErr)
			}
		}
	}()
	if err := sqlite.Migrate(startupCtx, db); err != nil {
		return fmt.Errorf("initialize schema: %w", err)
	}
	cancel()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("start HTTP listener: %w", err)
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen HTTP on %s: %w", cfg.Addr, err)
	}
	logger.Info("HTTP server started", "addr", ln.Addr().String())
	service := knowledge.NewService(sqlite.NewRepository(db))
	return httpapi.Serve(ctx, ln, httpapi.NewKnowledgeHandler(db, service, logger), logger, 10*time.Second)
}
