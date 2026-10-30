// Package token defines API credential hashing and authentication errors.
package token

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// Grant is the project whitelist carried by one valid token.
// Enforce false is only for callers that intentionally skip project checks.
type Grant struct {
	Projects []string
	Enforce  bool
}

// Authenticator checks an Authorization header value.
type Authenticator interface {
	Authenticate(ctx context.Context, authorization string, now time.Time) (Grant, error)
}

// ErrUnauthenticated is returned for a missing, unknown, revoked, or expired credential.
// Callers must not distinguish these cases in HTTP responses or logs.
var ErrUnauthenticated = errors.New("unauthenticated")

// Credential is a stored token without its secret or hash.
type Credential struct {
	ID        string
	Name      string
	Projects  []string
	CreatedAt time.Time
	ExpiresAt *time.Time
	RevokedAt *time.Time
}

// IssuedCredential is returned once, when a secret is created.
type IssuedCredential struct {
	Credential
	Token string
}

// Hash returns the hex-encoded SHA-256 of a token secret.
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
