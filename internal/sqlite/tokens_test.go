package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chiisen/agent-knowledge-hub/internal/token"
)

func TestTokenIssueAuthenticateRevokeRotateAndExpiry(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := NewTokenStore(db)
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	issued, err := store.Issue(ctx, " laptop ", []string{" alpha ", "alpha"}, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || issued.Name != "laptop" || issued.ExpiresAt == nil || len(issued.Projects) != 1 || issued.Projects[0] != "alpha" {
		t.Fatalf("issued = %+v", issued)
	}
	var stored string
	if err := db.QueryRowContext(ctx, `SELECT token_hash FROM api_tokens WHERE id = ?`, issued.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == issued.Token || stored != token.Hash(issued.Token) {
		t.Fatal("plaintext was stored or hash mismatch")
	}
	if _, err := store.Authenticate(ctx, "Bearer "+issued.Token, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, "Bearer "+issued.Token, now.Add(time.Hour)); err != token.ErrUnauthenticated {
		t.Fatalf("expired token error = %v", err)
	}
	permanent, err := store.Issue(ctx, "permanent", []string{"hub"}, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if permanent.ExpiresAt != nil {
		t.Fatal("zero ttl stored an expiry")
	}
	if _, err := store.Authenticate(ctx, "Bearer "+permanent.Token, now.Add(365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"", "Bearer", "Token " + permanent.Token, "Bearer " + permanent.Token + " extra"} {
		if _, err := store.Authenticate(ctx, header, now); err != token.ErrUnauthenticated {
			t.Fatalf("header %q error = %v", header, err)
		}
	}
	rotated, err := store.Rotate(ctx, permanent.ID, nil, 0, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Token == permanent.Token || rotated.Name != "permanent" {
		t.Fatalf("rotated = %+v", rotated)
	}
	if _, err := store.Authenticate(ctx, "Bearer "+permanent.Token, now.Add(time.Second)); err != token.ErrUnauthenticated {
		t.Fatal("rotated token still authenticates")
	}
	grant, err := store.Authenticate(ctx, "Bearer "+rotated.Token, now.Add(time.Minute))
	if err != nil || len(grant.Projects) != 1 || grant.Projects[0] != "hub" {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, rotated.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, "Bearer "+rotated.Token, now.Add(2*time.Second)); err != token.ErrUnauthenticated {
		t.Fatal("revoked token still authenticates")
	}
	if err := store.Revoke(ctx, rotated.ID, now); err == nil {
		t.Fatal("second revoke succeeded")
	}
	items, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == "" || item.Name == "" {
			t.Fatalf("list item = %+v", item)
		}
	}
}
