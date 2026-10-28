// Package knowledge defines knowledge records and their lifecycle rules.
package knowledge

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid   = errors.New("invalid knowledge")
	ErrInvalidID = errors.New("invalid UUID")
	ErrNotFound  = errors.New("knowledge not found")
	ErrConflict  = errors.New("knowledge version conflict")
)

type Knowledge struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Content      string     `json:"content"`
	Type         string     `json:"type"`
	Scope        string     `json:"scope"`
	Project      *string    `json:"project"`
	Module       *string    `json:"module"`
	Status       string     `json:"status"`
	Source       string     `json:"source"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Version      int64      `json:"version"`
	ArchivedAt   *time.Time `json:"-"`
	UsableAsRule bool       `json:"usable_as_rule"`
}

type Create struct {
	Title   string
	Content string
	Type    string
	Scope   string
	Project *string
	Module  *string
	Status  string
	Source  string
}

// NullableString distinguishes an omitted PATCH field from an explicit null.
type NullableString struct {
	Set   bool
	Value *string
}
type Patch struct {
	Title, Content, Type, Scope, Status, Source *string
	Project, Module                             NullableString
}

type Filter struct {
	Project, Module, Type, Scope, Status string
	Limit, Offset                        int
}

type Page struct {
	Items   []Knowledge
	HasMore bool
}

type SearchResult struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Status  string `json:"status"`
	Source  string `json:"source"`
}

type SearchPage struct {
	Items   []SearchResult
	HasMore bool
}

// Repository exposes application types only, never SQLite or HTTP structures.
type Repository interface {
	Create(context.Context, Knowledge) (Knowledge, error)
	Get(context.Context, string) (Knowledge, error)
	List(context.Context, Filter) (Page, error)
	Search(context.Context, string, Filter) (SearchPage, error)
	Update(context.Context, Knowledge, int64) (Knowledge, error)
	Archive(context.Context, string, int64, time.Time) error
}
