package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var _ ICategoryRepository = (*SQLiteCategoryRepository)(nil)

// SQLiteCategoryRepository implements ICategoryRepository using a SQLite connection.
type SQLiteCategoryRepository struct {
	db *sql.DB
}

// NewSQLiteCategoryRepository creates a new category repository.
func NewSQLiteCategoryRepository(db *sql.DB) *SQLiteCategoryRepository {
	return &SQLiteCategoryRepository{db: db}
}

// Create inserts a new category.
func (r *SQLiteCategoryRepository) Create(ctx context.Context, cat *Category) error {
	cat.Name = strings.TrimSpace(cat.Name)
	cat.Directory = strings.TrimSpace(cat.Directory)

	if cat.Name == "" || len(cat.Name) > 80 {
		return fmt.Errorf("category name must be between 1 and 80 characters")
	}
	if cat.Directory == "" {
		return fmt.Errorf("category directory cannot be empty")
	}

	if cat.ID == "" {
		cat.ID = uuid.New().String()
	}

	now := time.Now()
	if cat.CreatedAt.IsZero() {
		cat.CreatedAt = now
	}
	cat.UpdatedAt = now

	query := `INSERT INTO categories (id, name, directory, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`
	_, err := r.db.ExecContext(ctx, query, cat.ID, cat.Name, cat.Directory, cat.CreatedAt, cat.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "idx_categories_name") {
			return fmt.Errorf("category name already exists: %w", err)
		}
		return fmt.Errorf("insert category: %w", err)
	}

	return nil
}

// GetByID fetches a category by ID.
func (r *SQLiteCategoryRepository) GetByID(ctx context.Context, id string) (*Category, error) {
	query := `SELECT id, name, directory, created_at, updated_at FROM categories WHERE id = ?`
	row := r.db.QueryRowContext(ctx, query, id)

	var cat Category
	err := row.Scan(&cat.ID, &cat.Name, &cat.Directory, &cat.CreatedAt, &cat.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get category by id: %w", err)
	}
	return &cat, nil
}

// GetByName fetches a category by case-insensitive name.
func (r *SQLiteCategoryRepository) GetByName(ctx context.Context, name string) (*Category, error) {
	query := `SELECT id, name, directory, created_at, updated_at FROM categories WHERE LOWER(name) = LOWER(?)`
	row := r.db.QueryRowContext(ctx, query, strings.TrimSpace(name))

	var cat Category
	err := row.Scan(&cat.ID, &cat.Name, &cat.Directory, &cat.CreatedAt, &cat.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get category by name: %w", err)
	}
	return &cat, nil
}

// List returns all categories ordered by name.
func (r *SQLiteCategoryRepository) List(ctx context.Context) ([]Category, error) {
	query := `SELECT id, name, directory, created_at, updated_at FROM categories ORDER BY name ASC`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	var categories []Category
	for rows.Next() {
		var cat Category
		if err := rows.Scan(&cat.ID, &cat.Name, &cat.Directory, &cat.CreatedAt, &cat.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		categories = append(categories, cat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error listing categories: %w", err)
	}
	return categories, nil
}

// Update updates an existing category.
func (r *SQLiteCategoryRepository) Update(ctx context.Context, cat *Category) error {
	cat.Name = strings.TrimSpace(cat.Name)
	cat.Directory = strings.TrimSpace(cat.Directory)

	if cat.Name == "" || len(cat.Name) > 80 {
		return fmt.Errorf("category name must be between 1 and 80 characters")
	}
	if cat.Directory == "" {
		return fmt.Errorf("category directory cannot be empty")
	}

	cat.UpdatedAt = time.Now()

	query := `UPDATE categories SET name = ?, directory = ?, updated_at = ? WHERE id = ?`
	res, err := r.db.ExecContext(ctx, query, cat.Name, cat.Directory, cat.UpdatedAt, cat.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "idx_categories_name") {
			return fmt.Errorf("category name already exists: %w", err)
		}
		return fmt.Errorf("update category: %w", err)
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("category not found")
	}

	return nil
}

// Delete removes a category by ID.
func (r *SQLiteCategoryRepository) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM categories WHERE id = ?`
	res, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("category not found")
	}

	return nil
}
