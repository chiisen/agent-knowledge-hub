package knowledge

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Service struct{ repo Repository }

func NewService(repo Repository) *Service { return &Service{repo: repo} }

func normalizeID(id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || len(id) != 36 || parsed.String() != strings.ToLower(id) {
		return "", ErrInvalidID
	}
	return parsed.String(), nil
}

func (s *Service) Create(ctx context.Context, input Create) (Knowledge, error) {
	if err := ctx.Err(); err != nil {
		return Knowledge{}, err
	}
	if input.Status == "" {
		input.Status = "draft"
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	id, err := uuid.NewRandom()
	if err != nil {
		return Knowledge{}, fmt.Errorf("generate knowledge UUID: %w", err)
	}
	k := Knowledge{ID: id.String(), Title: input.Title, Content: input.Content, Type: input.Type, Scope: input.Scope,
		Project: input.Project, Module: input.Module, Status: input.Status, Source: input.Source, CreatedAt: now, UpdatedAt: now, Version: 1}
	if err := validate(k); err != nil {
		return Knowledge{}, err
	}
	return s.repo.Create(ctx, k)
}

func (s *Service) Get(ctx context.Context, id string) (Knowledge, error) {
	if err := ctx.Err(); err != nil {
		return Knowledge{}, err
	}
	id, err := normalizeID(id)
	if err != nil {
		return Knowledge{}, err
	}
	k, err := s.repo.Get(ctx, id)
	if err != nil {
		return Knowledge{}, err
	}
	if k.ArchivedAt != nil {
		return Knowledge{}, ErrNotFound
	}
	return k, nil
}

func (s *Service) List(ctx context.Context, f Filter) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if f.Limit == 0 {
		f.Limit = 20
	}
	if f.Status == "" {
		f.Status = "confirmed"
	}
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.Offset > 10000 {
		return Page{}, fmt.Errorf("%w: pagination is out of range", ErrInvalid)
	}
	if !validStatus(f.Status) {
		return Page{}, fmt.Errorf("%w: unsupported status", ErrInvalid)
	}
	for _, v := range []string{f.Project, f.Module, f.Type, f.Scope} {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 128 {
			return Page{}, fmt.Errorf("%w: filter exceeds 128 characters", ErrInvalid)
		}
	}
	return s.repo.List(ctx, f)
}

func normalizeSearchQuery(query string) (string, error) {
	if !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') || strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > 256 {
		return "", fmt.Errorf("%w: query is required and must be at most 256 characters", ErrInvalid)
	}
	terms := strings.Fields(query)
	if len(terms) == 0 || len(terms) > 8 {
		return "", fmt.Errorf("%w: query must contain 1 to 8 terms", ErrInvalid)
	}
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		if utf8.RuneCountInString(term) < 3 {
			return "", fmt.Errorf("%w: each search term must contain at least 3 characters", ErrInvalid)
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " AND "), nil
}

func (s *Service) Search(ctx context.Context, query string, f Filter) (SearchPage, error) {
	if err := ctx.Err(); err != nil {
		return SearchPage{}, err
	}
	normalized, err := normalizeSearchQuery(query)
	if err != nil {
		return SearchPage{}, err
	}
	if f.Limit == 0 {
		f.Limit = 20
	}
	if f.Status == "" {
		f.Status = "confirmed"
	}
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.Offset > 10000 {
		return SearchPage{}, fmt.Errorf("%w: pagination is out of range", ErrInvalid)
	}
	if !validStatus(f.Status) {
		return SearchPage{}, fmt.Errorf("%w: unsupported status", ErrInvalid)
	}
	for _, v := range []string{f.Project, f.Module, f.Type, f.Scope} {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 128 {
			return SearchPage{}, fmt.Errorf("%w: filter exceeds 128 characters", ErrInvalid)
		}
	}
	return s.repo.Search(ctx, normalized, f)
}

func (s *Service) Update(ctx context.Context, id string, version int64, p Patch) (Knowledge, error) {
	if err := ctx.Err(); err != nil {
		return Knowledge{}, err
	}
	if version < 1 {
		return Knowledge{}, fmt.Errorf("%w: version must be positive", ErrInvalid)
	}
	k, err := s.Get(ctx, id)
	if err != nil {
		return Knowledge{}, err
	}
	if k.Version != version {
		return Knowledge{}, ErrConflict
	}
	updated, err := applyPatch(k, p)
	if err != nil {
		return Knowledge{}, err
	}
	updated.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	return s.repo.Update(ctx, updated, version)
}

func (s *Service) Archive(ctx context.Context, id string, version int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if version < 1 {
		return fmt.Errorf("%w: version must be positive", ErrInvalid)
	}
	id, err := normalizeID(id)
	if err != nil {
		return err
	}
	k, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	// Valid repeated DELETE is idempotent even with the pre-archive ETag.
	if k.ArchivedAt != nil {
		return nil
	}
	if k.Version != version {
		return ErrConflict
	}
	return s.repo.Archive(ctx, id, version, time.Now().UTC().Truncate(time.Millisecond))
}

func applyPatch(k Knowledge, p Patch) (Knowledge, error) {
	if p.Title == nil && p.Content == nil && p.Type == nil && p.Scope == nil && p.Status == nil && p.Source == nil && !p.Project.Set && !p.Module.Set {
		return Knowledge{}, fmt.Errorf("%w: patch must contain a mutable field", ErrInvalid)
	}
	changed := false
	for _, pair := range []struct {
		dst *string
		src *string
	}{{&k.Title, p.Title}, {&k.Content, p.Content}, {&k.Type, p.Type}, {&k.Scope, p.Scope}, {&k.Source, p.Source}} {
		if pair.src != nil {
			changed = changed || *pair.dst != *pair.src
			*pair.dst = *pair.src
		}
	}
	for _, pair := range []struct {
		dst **string
		src NullableString
	}{{&k.Project, p.Project}, {&k.Module, p.Module}} {
		if pair.src.Set {
			changed = changed || !equalNullable(*pair.dst, pair.src.Value)
			*pair.dst = pair.src.Value
		}
	}
	if k.Status == "confirmed" && changed {
		if p.Status != nil && *p.Status != "draft" {
			return Knowledge{}, fmt.Errorf("%w: changed confirmed knowledge must become draft before another status transition", ErrInvalid)
		}
		k.Status = "draft"
	}
	if p.Status != nil {
		k.Status = *p.Status
	}
	if err := validate(k); err != nil {
		return Knowledge{}, err
	}
	return k, nil
}

func equalNullable(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
func validStatus(s string) bool { return s == "draft" || s == "confirmed" || s == "deprecated" }
func validate(k Knowledge) error {
	for _, field := range []struct {
		name, value string
		max         int
		bytes       bool
	}{
		{"title", k.Title, 256, false}, {"content", k.Content, 65536, true}, {"type", k.Type, 128, false},
		{"scope", k.Scope, 128, false}, {"source", k.Source, 16384, true},
	} {
		size := utf8.RuneCountInString(field.value)
		if field.bytes {
			size = len(field.value)
		}
		if !utf8.ValidString(field.value) || strings.TrimSpace(field.value) == "" || size > field.max {
			return fmt.Errorf("%w: %s is required and must fit its size limit", ErrInvalid, field.name)
		}
	}
	for _, v := range []*string{k.Project, k.Module} {
		if v != nil && (!utf8.ValidString(*v) || strings.TrimSpace(*v) == "" || utf8.RuneCountInString(*v) > 128) {
			return fmt.Errorf("%w: project/module must be null or nonblank and at most 128 characters", ErrInvalid)
		}
	}
	if !validStatus(k.Status) {
		return fmt.Errorf("%w: unsupported status", ErrInvalid)
	}
	return nil
}
