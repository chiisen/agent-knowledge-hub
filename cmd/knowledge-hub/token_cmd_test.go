package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chiisen/agent-knowledge-hub/internal/sqlite"
	"github.com/chiisen/agent-knowledge-hub/internal/token"
)

func TestTokenCLIIssueListRotateRevoke(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "knowledge.db")
	var issued bytes.Buffer
	if err := runToken(context.Background(), []string{"issue", "-db", dbPath, "-name", "laptop"}, &issued); err != nil {
		t.Fatal(err)
	}
	var first struct {
		ID    string `json:"id"`
		Token string `json:"token"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(issued.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Token == "" || first.Name != "laptop" || strings.Contains(issued.String(), token.Hash(first.Token)) {
		t.Fatalf("issued output = %s", issued.String())
	}
	ctx := context.Background()
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlite.NewTokenStore(db).Authenticate(ctx, "Bearer "+first.Token, time.Now()); err != nil {
		t.Fatal(err)
	}
	var listed bytes.Buffer
	if err := runToken(ctx, []string{"list", "-db", dbPath}, &listed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listed.String(), first.Token) {
		t.Fatalf("list leaked secret: %s", listed.String())
	}
	var rotated bytes.Buffer
	if err := runToken(ctx, []string{"rotate", "-db", dbPath, "-id", first.ID, "-ttl", "1h"}, &rotated); err != nil {
		t.Fatal(err)
	}
	var second struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rotated.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.Token == "" || second.Token == first.Token {
		t.Fatalf("rotated = %s", rotated.String())
	}
	if err := sqlite.NewTokenStore(db).Authenticate(ctx, "Bearer "+first.Token, time.Now()); err != token.ErrUnauthenticated {
		t.Fatal("old token still valid after rotate")
	}
	if err := runToken(ctx, []string{"revoke", "-db", dbPath, "-id", first.ID}, &bytes.Buffer{}); err == nil {
		t.Fatal("revoking the already rotated token succeeded")
	}
}
