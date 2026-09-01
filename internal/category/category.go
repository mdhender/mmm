// Copyright (c) 2026 Michael D Henderson.

// Package category reads and writes the categories a split can be assigned to.
//
// Categories are deliberately a flat list of names. The register needs them to
// label a row (SPECIFICATION.md RG-1) and reports need them to group spending
// (RP-1); a hierarchy, budgets, or rules would each have to answer SC-1 first.
package category

import (
	"context"
	"fmt"
	"strings"
	"time"

	"zombiezen.com/go/sqlite"

	"github.com/mdhender/mmm/internal/cerrs"
	"github.com/mdhender/mmm/internal/storage"
)

const (
	// ErrNotFound is returned when no category has the requested id.
	ErrNotFound = cerrs.Error("category not found")

	// ErrMissingName is returned when a category is created without a name.
	ErrMissingName = cerrs.Error("missing category name")
)

// Category is one spending or income category.
type Category struct {
	ID        int64
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const columns = `id, name, created_at, updated_at`

// List returns every category in name order.
func List(ctx context.Context, store *storage.Store) ([]Category, error) {
	conn, err := store.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer store.Put(conn)

	stmt, err := conn.Prepare(`SELECT ` + columns + ` FROM categories ORDER BY name;`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer stmt.Reset()

	var categories []Category
	for {
		hasRow, err := stmt.Step()
		if err != nil {
			return nil, fmt.Errorf("list categories: %w", err)
		}
		if !hasRow {
			return categories, nil
		}
		c, err := scan(stmt)
		if err != nil {
			return nil, fmt.Errorf("list categories: %w", err)
		}
		categories = append(categories, c)
	}
}

// Ensure returns the category with this name, creating it if it does not exist.
//
// Names are compared case-insensitively because the column is COLLATE NOCASE, so
// "Groceries" and "groceries" are one category rather than two that silently
// split a year of spending in half. The stored spelling is whichever was entered
// first; Ensure does not rewrite it.
func Ensure(ctx context.Context, store *storage.Store, name string) (Category, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Category{}, ErrMissingName
	}

	conn, err := store.Conn(ctx)
	if err != nil {
		return Category{}, fmt.Errorf("ensure category %q: %w", name, err)
	}
	defer store.Put(conn)

	// ON CONFLICT DO NOTHING would not return the existing row, and DO UPDATE
	// would rewrite the stored spelling, so the lookup comes first.
	stmt, err := conn.Prepare(`SELECT ` + columns + ` FROM categories WHERE name = $name;`)
	if err != nil {
		return Category{}, fmt.Errorf("ensure category %q: %w", name, err)
	}
	stmt.SetText("$name", name)
	hasRow, err := stmt.Step()
	if err != nil {
		_ = stmt.Reset()
		return Category{}, fmt.Errorf("ensure category %q: %w", name, err)
	}
	if hasRow {
		c, err := scan(stmt)
		_ = stmt.Reset()
		if err != nil {
			return Category{}, fmt.Errorf("ensure category %q: %w", name, err)
		}
		return c, nil
	}
	if err := stmt.Reset(); err != nil {
		return Category{}, fmt.Errorf("ensure category %q: %w", name, err)
	}

	insert, err := conn.Prepare(`
INSERT INTO categories (name) VALUES ($name)
RETURNING ` + columns + `;`)
	if err != nil {
		return Category{}, fmt.Errorf("ensure category %q: %w", name, err)
	}
	defer insert.Reset()
	insert.SetText("$name", name)

	hasRow, err = insert.Step()
	if err != nil {
		return Category{}, fmt.Errorf("ensure category %q: %w", name, err)
	}
	if !hasRow {
		return Category{}, fmt.Errorf("ensure category %q: insert returned no row", name)
	}
	c, err := scan(insert)
	if err != nil {
		return Category{}, fmt.Errorf("ensure category %q: %w", name, err)
	}
	return c, nil
}

// scan reads one row selected with columns.
func scan(stmt *sqlite.Stmt) (Category, error) {
	c := Category{
		ID:   stmt.GetInt64("id"),
		Name: stmt.GetText("name"),
	}

	var err error
	if c.CreatedAt, err = storage.ColumnTime(stmt, "created_at"); err != nil {
		return Category{}, err
	}
	if c.UpdatedAt, err = storage.ColumnTime(stmt, "updated_at"); err != nil {
		return Category{}, err
	}
	return c, nil
}
