package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	err := run(context.Background(), []string{"-addr", "0.0.0.0:8080"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("invalid configuration error = %v", err)
	}
}

func TestRunReturnsDatabaseInitializationError(t *testing.T) {
	err := run(context.Background(), []string{"-db", t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "SQLite") {
		t.Fatalf("initialization error = %v", err)
	}
}

func TestRunHonorsCanceledStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := run(ctx, []string{"-db", filepath.Join(t.TempDir(), "knowledge.db")}, slog.Default())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup error = %v", err)
	}
}
