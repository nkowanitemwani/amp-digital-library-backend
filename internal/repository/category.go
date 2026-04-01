package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
)

// CategoryRepository handles all database operations for the categories table.
type CategoryRepository struct {
	db *sql.DB
}

// NewCategoryRepository creates a CategoryRepository with the shared DB pool.
func NewCategoryRepository(db *sql.DB) *CategoryRepository {
	return &CategoryRepository{db: db}
}

// Create inserts a new category for a school and returns the created row.
// The UNIQUE constraint on (school_id, name) in the DB will reject
// duplicate category names for the same school — the service layer
// translates that DB error into a meaningful API error.
func (r *CategoryRepository) Create(ctx context.Context, schoolID, name string) (*models.Category, error) {
	query := `
		INSERT INTO categories (school_id, name)
		VALUES ($1, $2)
		RETURNING id, school_id, name, created_at, updated_at`

	cat := &models.Category{}
	err := r.db.QueryRowContext(ctx, query, schoolID, name).Scan(
		&cat.ID,
		&cat.SchoolID,
		&cat.Name,
		&cat.CreatedAt,
		&cat.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create category: %w", err)
	}

	return cat, nil
}

// GetAllBySchool returns every category belonging to a school,
// ordered alphabetically by name.
// Returns an empty slice (not nil) when the school has no categories
// so the API always returns a JSON array, never null.
func (r *CategoryRepository) GetAllBySchool(ctx context.Context, schoolID string) ([]*models.Category, error) {
	query := `
		SELECT id, school_id, name, created_at, updated_at
		FROM categories
		WHERE school_id = $1
		ORDER BY name ASC`

	rows, err := r.db.QueryContext(ctx, query, schoolID)
	if err != nil {
		return nil, fmt.Errorf("get categories: %w", err)
	}
	// Always close rows when done — failing to do so leaks the DB connection
	// back to the pool in a broken state.
	defer rows.Close()

	// Pre-allocate as empty slice so JSON serialises to [] not null
	// when there are no rows.
	categories := []*models.Category{}
	for rows.Next() {
		cat := &models.Category{}
		if err := rows.Scan(
			&cat.ID,
			&cat.SchoolID,
			&cat.Name,
			&cat.CreatedAt,
			&cat.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		categories = append(categories, cat)
	}

	// rows.Err() captures any error that occurred during iteration —
	// a plain loop exit does not guarantee the query completed cleanly.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate categories: %w", err)
	}

	return categories, nil
}

// GetByID fetches a single category by its primary key.
// schoolID is required — it ensures a school can only fetch their
// own categories even if they somehow supply another school's category ID.
func (r *CategoryRepository) GetByID(ctx context.Context, id, schoolID string) (*models.Category, error) {
	query := `
		SELECT id, school_id, name, created_at, updated_at
		FROM categories
		WHERE id = $1 AND school_id = $2`

	cat := &models.Category{}
	err := r.db.QueryRowContext(ctx, query, id, schoolID).Scan(
		&cat.ID,
		&cat.SchoolID,
		&cat.Name,
		&cat.CreatedAt,
		&cat.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get category by id: %w", err)
	}

	return cat, nil
}

// Delete removes a category by ID.
// schoolID is required for the same ownership reason as GetByID.
// The DB RESTRICT foreign key on books.category_id means this DELETE
// will fail if any books still reference this category — the service
// layer surfaces that as a meaningful error to the client.
func (r *CategoryRepository) Delete(ctx context.Context, id, schoolID string) error {
	query := `
		DELETE FROM categories
		WHERE id = $1 AND school_id = $2`

	result, err := r.db.ExecContext(ctx, query, id, schoolID)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete category rows affected: %w", err)
	}

	// Zero rows affected means the category either doesn't exist or
	// belongs to a different school — both cases are "not found" to the caller.
	if rows == 0 {
		return ErrNotFound
	}

	return nil
}