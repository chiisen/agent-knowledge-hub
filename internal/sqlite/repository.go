package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chiisen/agent-knowledge-hub/internal/knowledge"
)

type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

var _ knowledge.Repository = (*Repository)(nil)

const columns = "id,title,content,type,scope,project,module,status,source,created_at,updated_at,version,archived_at"

type scanner interface{ Scan(...any) error }

func scanKnowledge(row scanner) (knowledge.Knowledge, error) {
	var k knowledge.Knowledge
	var created, updated int64
	var archived sql.NullInt64
	if err := row.Scan(&k.ID, &k.Title, &k.Content, &k.Type, &k.Scope, &k.Project, &k.Module, &k.Status, &k.Source, &created, &updated, &k.Version, &archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return k, knowledge.ErrNotFound
		}
		return k, err
	}
	k.CreatedAt = time.UnixMilli(created).UTC()
	k.UpdatedAt = time.UnixMilli(updated).UTC()
	if archived.Valid {
		stamp := time.UnixMilli(archived.Int64).UTC()
		k.ArchivedAt = &stamp
	}
	k.UsableAsRule = k.Status == "confirmed" && k.ArchivedAt == nil
	return k, nil
}

func (r *Repository) Create(ctx context.Context, k knowledge.Knowledge) (knowledge.Knowledge, error) {
	row := r.db.QueryRowContext(ctx, `INSERT INTO knowledge(id,title,content,type,scope,project,module,status,source,created_at,updated_at)
  VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING `+columns, k.ID, k.Title, k.Content, k.Type, k.Scope, k.Project, k.Module, k.Status, k.Source, k.CreatedAt.UnixMilli(), k.UpdatedAt.UnixMilli())
	got, err := scanKnowledge(row)
	if err != nil {
		return got, fmt.Errorf("insert knowledge: %w", err)
	}
	return got, nil
}
func (r *Repository) Get(ctx context.Context, id string) (knowledge.Knowledge, error) {
	k, err := scanKnowledge(r.db.QueryRowContext(ctx, "SELECT "+columns+" FROM knowledge WHERE id = ?", id))
	if err != nil {
		return k, fmt.Errorf("select knowledge: %w", err)
	}
	return k, nil
}
func (r *Repository) List(ctx context.Context, f knowledge.Filter) (knowledge.Page, error) {
	var query strings.Builder
	query.WriteString("SELECT " + columns + " FROM knowledge WHERE archived_at IS NULL")
	var args []any
	// Column names are compiled-in, never supplied by the request.
	for _, filter := range []struct{ name, value string }{{"project", f.Project}, {"module", f.Module}, {"type", f.Type}, {"scope", f.Scope}, {"status", f.Status}} {
		if filter.value != "" {
			query.WriteString(" AND " + filter.name + " = ?")
			args = append(args, filter.value)
		}
	}
	query.WriteString(" ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?")
	args = append(args, f.Limit+1, f.Offset)
	rows, err := r.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return knowledge.Page{}, fmt.Errorf("list knowledge: %w", err)
	}
	defer rows.Close()
	page := knowledge.Page{Items: make([]knowledge.Knowledge, 0, f.Limit)}
	for rows.Next() {
		k, err := scanKnowledge(rows)
		if err != nil {
			return page, fmt.Errorf("scan knowledge list: %w", err)
		}
		page.Items = append(page.Items, k)
	}
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("iterate knowledge list: %w", err)
	}
	page.HasMore = len(page.Items) > f.Limit
	if page.HasMore {
		page.Items = page.Items[:f.Limit]
	}
	return page, nil
}
func (r *Repository) Update(ctx context.Context, k knowledge.Knowledge, version int64) (knowledge.Knowledge, error) {
	got, err := scanKnowledge(r.db.QueryRowContext(ctx, `UPDATE knowledge SET title=?,content=?,type=?,scope=?,project=?,module=?,status=?,source=?,updated_at=?,version=version+1
  WHERE id=? AND version=? AND archived_at IS NULL RETURNING `+columns, k.Title, k.Content, k.Type, k.Scope, k.Project, k.Module, k.Status, k.Source, k.UpdatedAt.UnixMilli(), k.ID, version))
	if errors.Is(err, knowledge.ErrNotFound) {
		return got, knowledge.ErrConflict
	}
	if err != nil {
		return got, fmt.Errorf("update knowledge: %w", err)
	}
	return got, nil
}
func (r *Repository) Archive(ctx context.Context, id string, version int64, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE knowledge SET archived_at=?,updated_at=?,version=version+1 WHERE id=? AND version=? AND archived_at IS NULL`, at.UnixMilli(), at.UnixMilli(), id, version)
	if err != nil {
		return fmt.Errorf("archive knowledge: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count archived knowledge: %w", err)
	}
	if count != 1 {
		return knowledge.ErrConflict
	}
	return nil
}
