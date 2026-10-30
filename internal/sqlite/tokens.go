package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chiisen/agent-knowledge-hub/internal/token"
	"github.com/google/uuid"
)

// TokenStore persists API token hashes. Plaintext secrets are never stored.
type TokenStore struct{ db *sql.DB }

func NewTokenStore(db *sql.DB) *TokenStore { return &TokenStore{db: db} }

var _ token.Authenticator = (*TokenStore)(nil)

// Authenticate accepts an Authorization header value. Missing, unknown, revoked,
// and expired secrets return the same error.
func (s *TokenStore) Authenticate(ctx context.Context, authorization string, now time.Time) error {
	secret, err := bearerSecret(authorization)
	if err != nil {
		return err
	}
	var expires, revoked sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT expires_at, revoked_at FROM api_tokens WHERE token_hash = ?`, token.Hash(secret)).Scan(&expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return token.ErrUnauthenticated
	}
	if err != nil {
		return fmt.Errorf("authenticate API token: %w", err)
	}
	if revoked.Valid || (expires.Valid && expires.Int64 <= now.UTC().UnixMilli()) {
		return token.ErrUnauthenticated
	}
	return nil
}

// Issue creates a token. ttl <= 0 means the token does not expire.
func (s *TokenStore) Issue(ctx context.Context, name string, ttl time.Duration, now time.Time) (token.IssuedCredential, error) {
	return s.insert(ctx, nil, name, ttl, now)
}

// Rotate revokes the named token and issues a replacement with the same name.
func (s *TokenStore) Rotate(ctx context.Context, id string, ttl time.Duration, now time.Time) (token.IssuedCredential, error) {
	return s.insert(ctx, &id, "", ttl, now)
}

// Revoke marks a token unusable. Revoking an unknown or already revoked token fails.
func (s *TokenStore) Revoke(ctx context.Context, id string, now time.Time) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("revoke API token: id must be a UUID")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now.UTC().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("revoke API token: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke API token: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("revoke API token: active token not found")
	}
	return nil
}

// List returns token metadata. It never returns secrets or hashes.
func (s *TokenStore) List(ctx context.Context) ([]token.Credential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at, expires_at, revoked_at FROM api_tokens ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list API tokens: %w", err)
	}
	defer rows.Close()
	var out []token.Credential
	for rows.Next() {
		item, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list API tokens: %w", err)
	}
	return out, nil
}

func (s *TokenStore) insert(ctx context.Context, rotateID *string, name string, ttl time.Duration, now time.Time) (token.IssuedCredential, error) {
	now = now.UTC()
	if ttl < 0 {
		return token.IssuedCredential{}, fmt.Errorf("issue API token: ttl must not be negative")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return token.IssuedCredential{}, fmt.Errorf("issue API token: %w", err)
	}
	defer tx.Rollback()
	if rotateID != nil {
		if _, err := uuid.Parse(*rotateID); err != nil {
			return token.IssuedCredential{}, fmt.Errorf("rotate API token: id must be a UUID")
		}
		if err := tx.QueryRowContext(ctx, `SELECT name FROM api_tokens WHERE id = ? AND revoked_at IS NULL`, *rotateID).Scan(&name); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return token.IssuedCredential{}, fmt.Errorf("rotate API token: active token not found")
			}
			return token.IssuedCredential{}, fmt.Errorf("rotate API token: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now.UnixMilli(), *rotateID); err != nil {
			return token.IssuedCredential{}, fmt.Errorf("rotate API token: %w", err)
		}
	}
	name, err = normalizeName(name)
	if err != nil {
		return token.IssuedCredential{}, err
	}
	secret, err := newSecret()
	if err != nil {
		return token.IssuedCredential{}, err
	}
	issued := token.IssuedCredential{Credential: token.Credential{ID: uuid.NewString(), Name: name, CreatedAt: now}, Token: secret}
	var expires any
	if ttl > 0 {
		exp := now.Add(ttl)
		issued.ExpiresAt = &exp
		expires = exp.UnixMilli()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO api_tokens(id, name, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		issued.ID, issued.Name, token.Hash(secret), now.UnixMilli(), expires); err != nil {
		return token.IssuedCredential{}, fmt.Errorf("issue API token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return token.IssuedCredential{}, fmt.Errorf("issue API token: %w", err)
	}
	return issued, nil
}

func bearerSecret(authorization string) (string, error) {
	scheme, secret, ok := strings.Cut(authorization, " ")
	if !ok || scheme != "Bearer" || secret == "" || strings.Contains(secret, " ") {
		return "", token.ErrUnauthenticated
	}
	return secret, nil
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 128 {
		return "", fmt.Errorf("issue API token: name must be 1 to 128 characters")
	}
	return name, nil
}

func newSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("issue API token: %w", err)
	}
	return "kh_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCredential(row rowScanner) (token.Credential, error) {
	var item token.Credential
	var created int64
	var expires, revoked sql.NullInt64
	if err := row.Scan(&item.ID, &item.Name, &created, &expires, &revoked); err != nil {
		return token.Credential{}, fmt.Errorf("scan API token: %w", err)
	}
	item.CreatedAt = time.UnixMilli(created).UTC()
	if expires.Valid {
		t := time.UnixMilli(expires.Int64).UTC()
		item.ExpiresAt = &t
	}
	if revoked.Valid {
		t := time.UnixMilli(revoked.Int64).UTC()
		item.RevokedAt = &t
	}
	return item, nil
}
