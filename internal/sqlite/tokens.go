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
func (s *TokenStore) Authenticate(ctx context.Context, authorization string, now time.Time) (token.Grant, error) {
	secret, err := bearerSecret(authorization)
	if err != nil {
		return token.Grant{}, err
	}
	var id string
	var expires, revoked sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT id, expires_at, revoked_at FROM api_tokens WHERE token_hash = ?`, token.Hash(secret)).Scan(&id, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return token.Grant{}, token.ErrUnauthenticated
	}
	if err != nil {
		return token.Grant{}, fmt.Errorf("authenticate API token: %w", err)
	}
	if revoked.Valid || (expires.Valid && expires.Int64 <= now.UTC().UnixMilli()) {
		return token.Grant{}, token.ErrUnauthenticated
	}
	projects, err := loadProjects(ctx, s.db, id)
	if err != nil {
		return token.Grant{}, err
	}
	return token.Grant{Projects: projects, Enforce: true}, nil
}

// Issue creates a token. ttl <= 0 means the token does not expire.
// projects must contain at least one name.
func (s *TokenStore) Issue(ctx context.Context, name string, projects []string, ttl time.Duration, now time.Time) (token.IssuedCredential, error) {
	projects, err := normalizeProjects(projects)
	if err != nil {
		return token.IssuedCredential{}, err
	}
	return s.insert(ctx, nil, name, projects, ttl, now)
}

// Rotate revokes the named token and issues a replacement with the same name.
// An empty projects slice keeps the previous whitelist.
func (s *TokenStore) Rotate(ctx context.Context, id string, projects []string, ttl time.Duration, now time.Time) (token.IssuedCredential, error) {
	if len(projects) > 0 {
		projects, err := normalizeProjects(projects)
		if err != nil {
			return token.IssuedCredential{}, err
		}
		return s.insert(ctx, &id, "", projects, ttl, now)
	}
	return s.insert(ctx, &id, "", nil, ttl, now)
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
		item.Projects, err = loadProjects(ctx, s.db, item.ID)
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

func (s *TokenStore) insert(ctx context.Context, rotateID *string, name string, projects []string, ttl time.Duration, now time.Time) (token.IssuedCredential, error) {
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
		if len(projects) == 0 {
			projects, err = loadProjects(ctx, tx, *rotateID)
			if err != nil {
				return token.IssuedCredential{}, err
			}
			if len(projects) == 0 {
				return token.IssuedCredential{}, fmt.Errorf("rotate API token: active token has no projects")
			}
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
	issued := token.IssuedCredential{Credential: token.Credential{ID: uuid.NewString(), Name: name, Projects: projects, CreatedAt: now}, Token: secret}
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
	for _, project := range projects {
		if _, err := tx.ExecContext(ctx, `INSERT INTO api_token_projects(token_id, project) VALUES (?, ?)`, issued.ID, project); err != nil {
			return token.IssuedCredential{}, fmt.Errorf("issue API token projects: %w", err)
		}
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

type projectQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func loadProjects(ctx context.Context, q projectQueryer, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT project FROM api_token_projects WHERE token_id = ? ORDER BY project`, id)
	if err != nil {
		return nil, fmt.Errorf("read API token projects: %w", err)
	}
	defer rows.Close()
	var projects []string
	for rows.Next() {
		var project string
		if err := rows.Scan(&project); err != nil {
			return nil, fmt.Errorf("scan API token project: %w", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate API token projects: %w", err)
	}
	if projects == nil {
		projects = []string{}
	}
	return projects, nil
}

func normalizeProjects(projects []string) ([]string, error) {
	if len(projects) == 0 {
		return nil, fmt.Errorf("issue API token: at least one project is required")
	}
	seen := make(map[string]struct{}, len(projects))
	out := make([]string, 0, len(projects))
	for _, project := range projects {
		project = strings.TrimSpace(project)
		if project == "" || utf8.RuneCountInString(project) > 128 {
			return nil, fmt.Errorf("issue API token: project must be 1 to 128 characters")
		}
		if _, ok := seen[project]; ok {
			continue
		}
		seen[project] = struct{}{}
		out = append(out, project)
	}
	return out, nil
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
