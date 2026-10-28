// Package config parses the small command-line configuration surface.
package config

import (
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

type Config struct {
	Addr   string
	DBPath string
}

func Parse(args []string) (Config, error) {
	var cfg Config
	fs := flag.NewFlagSet("knowledge-hub", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Addr, "addr", "127.0.0.1:8080", "loopback HTTP address")
	fs.StringVar(&cfg.DBPath, "db", "data/knowledge.db", "SQLite file path")
	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse configuration: %w", err)
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("parse configuration: positional arguments are not supported")
	}
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return Config{}, fmt.Errorf("validate HTTP address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return Config{}, fmt.Errorf("validate HTTP address: only literal loopback IPs are supported in Milestone 1")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return Config{}, fmt.Errorf("validate HTTP address: port must be between 0 and 65535")
	}
	if strings.TrimSpace(cfg.DBPath) == "" {
		return Config{}, fmt.Errorf("validate database path: must not be empty")
	}
	return cfg, nil
}
