package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
)

// CategoryRepository handles all database operations for the categories table.
// Categories are scoped to a grade — every method takes a gradeID so a grade
// can never read or modify another grade's categories.
type CategoryRepository struct {
	db *sql.DB
}

// NewCategoryRepository creates a CategoryRepository with the shared DB pool.
func NewCategoryRepository(db *sql.DB) *CategoryRepository {
	return &CategoryRepository{db: db}
}

// Create inserts a new category for a grade and returns the created row.
// The UNIQUE constraint on (grade_id, name) in the DB will reject duplicate
// category names for the same grade.
func (r *CategoryRepository) Create(ctx context.Context, schoolID, gradeID, name string) (*models.Category, error) {
	query := `
		INSERT INTO categories (school_id, grade_id, name)
		VALUES ($1, $2, $3)
		RETURNING id, school_id, grade_id, name, created_at, updated_at`

	cat := &models.Category{}
	err := r.db.QueryRowContext(ctx, query, schoolID, gradeID, name).Scan(
		&cat.ID,
		&cat.SchoolID,
		&cat.GradeID,
		&cat.Name,
		&cat.CreatedAt,
		&cat.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create category: %w", err)
	}

	return cat, nil
}

// GetAllByGrade returns every category belonging to a grade,
// ordered alphabetically by name.
// Returns an empty slice (not nil) so the API always returns a JSON array.
func (r *CategoryRepository) GetAllByGrade(ctx context.Context, gradeID string) ([]*models.Category, error) {
	query := `
		SELECT id, school_id, grade_id, name, created_at, updated_at
		FROM categories
		WHERE grade_id = $1
		ORDER BY name ASC`

	rows, err := r.db.QueryContext(ctx, query, gradeID)
	if err != nil {
		return nil, fmt.Errorf("get categories: %w", err)
	}
	defer rows.Close()

	categories := []*models.Category{}
	for rows.Next() {
		cat := &models.Category{}
		if err := rows.Scan(
			&cat.ID, &cat.SchoolID, &cat.GradeID,
			&cat.Name, &cat.CreatedAt, &cat.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		categories = append(categories, cat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate categories: %w", err)
	}

	return categories, nil
}

// GetByID fetches a single category by its primary key.
// gradeID is required — ensures a grade can only fetch their own categories.
func (r *CategoryRepository) GetByID(ctx context.Context, id, gradeID string) (*models.Category, error) {
	query := `
		SELECT id, school_id, grade_id, name, created_at, updated_at
		FROM categories
		WHERE id = $1 AND grade_id = $2`

	cat := &models.Category{}
	err := r.db.QueryRowContext(ctx, query, id, gradeID).Scan(
		&cat.ID, &cat.SchoolID, &cat.GradeID,
		&cat.Name, &cat.CreatedAt, &cat.UpdatedAt,
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
// gradeID is required for ownership verification.
// The DB RESTRICT foreign key on books.category_id means this DELETE
// will fail if any books still reference this category.
func (r *CategoryRepository) Delete(ctx context.Context, id, gradeID string) error {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM categories WHERE id = $1 AND grade_id = $2`, id, gradeID,
	)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete category rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}

	return nil
}