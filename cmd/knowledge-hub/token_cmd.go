package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/chiisen/agent-knowledge-hub/internal/sqlite"
	"github.com/chiisen/agent-knowledge-hub/internal/token"
)

func runToken(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("token command requires issue, list, revoke, or rotate")
	}
	switch args[0] {
	case "issue", "list", "revoke", "rotate":
	default:
		return fmt.Errorf("unknown token command %q", args[0])
	}
	fs := flag.NewFlagSet("token "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "data/knowledge.db", "SQLite file path")
	name := fs.String("name", "", "token name")
	id := fs.String("id", "", "token id")
	ttl := fs.Duration("ttl", 0, "optional lifetime; zero means no expiry")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("parse token command: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("parse token command: positional arguments are not supported")
	}
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := sqlite.Open(startup, *dbPath)
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	defer db.Close()
	if err := sqlite.Migrate(startup, db); err != nil {
		return fmt.Errorf("initialize schema: %w", err)
	}
	store := sqlite.NewTokenStore(db)
	now := time.Now()
	switch args[0] {
	case "issue":
		issued, err := store.Issue(startup, *name, *ttl, now)
		if err != nil {
			return err
		}
		return writeIssued(stdout, issued)
	case "rotate":
		issued, err := store.Rotate(startup, *id, *ttl, now)
		if err != nil {
			return err
		}
		return writeIssued(stdout, issued)
	case "revoke":
		if err := store.Revoke(startup, *id, now); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "revoked")
		return nil
	default:
		items, err := store.List(startup)
		if err != nil {
			return err
		}
		payload := make([]credentialView, 0, len(items))
		for _, item := range items {
			payload = append(payload, newCredentialView(item))
		}
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(payload)
	}
}

type credentialView struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at"`
	Token     string     `json:"token,omitempty"`
}

func newCredentialView(item token.Credential) credentialView {
	return credentialView{ID: item.ID, Name: item.Name, CreatedAt: item.CreatedAt, ExpiresAt: item.ExpiresAt, RevokedAt: item.RevokedAt}
}

func writeIssued(stdout io.Writer, issued token.IssuedCredential) error {
	view := newCredentialView(issued.Credential)
	view.Token = issued.Token
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(view)
}
