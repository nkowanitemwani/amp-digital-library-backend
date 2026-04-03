package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
)

// GradeRepository handles all database operations for the grades table.
// Every method takes a schoolID so a school can never read or modify
// another school's grades, even if they know a grade's UUID.
type GradeRepository struct {
	db *sql.DB
}

// NewGradeRepository creates a GradeRepository with the shared DB pool.
func NewGradeRepository(db *sql.DB) *GradeRepository {
	return &GradeRepository{db: db}
}

// Create inserts a new grade and returns the created row.
// The UNIQUE constraint on (school_id, username) rejects duplicate
// usernames within the same school.
func (r *GradeRepository) Create(ctx context.Context, grade *models.Grade) (*models.Grade, error) {
	query := `
		INSERT INTO grades (school_id, name, username, password_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING id, school_id, name, username, password_hash,
		          is_active, login_attempts, locked_until, last_login_at,
		          created_at, updated_at`

	g := &models.Grade{}
	err := r.db.QueryRowContext(ctx, query,
		grade.SchoolID,
		grade.Name,
		grade.Username,
		grade.PasswordHash,
	).Scan(
		&g.ID, &g.SchoolID, &g.Name, &g.Username, &g.PasswordHash,
		&g.IsActive, &g.LoginAttempts, &g.LockedUntil, &g.LastLoginAt,
		&g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create grade: %w", err)
	}

	return g, nil
}

// GetByID fetches a single grade by primary key.
// schoolID is required — a school can only fetch their own grades.
func (r *GradeRepository) GetByID(ctx context.Context, id, schoolID string) (*models.Grade, error) {
	query := `
		SELECT id, school_id, name, username, password_hash,
		       is_active, login_attempts, locked_until, last_login_at,
		       created_at, updated_at
		FROM grades
		WHERE id = $1 AND school_id = $2`

	g := &models.Grade{}
	err := r.db.QueryRowContext(ctx, query, id, schoolID).Scan(
		&g.ID, &g.SchoolID, &g.Name, &g.Username, &g.PasswordHash,
		&g.IsActive, &g.LoginAttempts, &g.LockedUntil, &g.LastLoginAt,
		&g.CreatedAt, &g.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get grade by id: %w", err)
	}

	return g, nil
}

// GetByUsername fetches a grade by school + username for the login flow.
// Both fields are required because usernames are only unique within a school.
func (r *GradeRepository) GetByUsername(ctx context.Context, schoolID, username string) (*models.Grade, error) {
	query := `
		SELECT id, school_id, name, username, password_hash,
		       is_active, login_attempts, locked_until, last_login_at,
		       created_at, updated_at
		FROM grades
		WHERE school_id = $1 AND username = $2`

	g := &models.Grade{}
	err := r.db.QueryRowContext(ctx, query, schoolID, username).Scan(
		&g.ID, &g.SchoolID, &g.Name, &g.Username, &g.PasswordHash,
		&g.IsActive, &g.LoginAttempts, &g.LockedUntil, &g.LastLoginAt,
		&g.CreatedAt, &g.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get grade by username: %w", err)
	}

	return g, nil
}

// GetAllBySchool returns every grade in a school ordered by name.
// Returns an empty slice (not nil) so the API always returns a JSON array.
func (r *GradeRepository) GetAllBySchool(ctx context.Context, schoolID string) ([]*models.Grade, error) {
	query := `
		SELECT id, school_id, name, username, password_hash,
		       is_active, login_attempts, locked_until, last_login_at,
		       created_at, updated_at
		FROM grades
		WHERE school_id = $1
		ORDER BY name ASC`

	rows, err := r.db.QueryContext(ctx, query, schoolID)
	if err != nil {
		return nil, fmt.Errorf("get grades by school: %w", err)
	}
	defer rows.Close()

	grades := []*models.Grade{}
	for rows.Next() {
		g := &models.Grade{}
		if err := rows.Scan(
			&g.ID, &g.SchoolID, &g.Name, &g.Username, &g.PasswordHash,
			&g.IsActive, &g.LoginAttempts, &g.LockedUntil, &g.LastLoginAt,
			&g.CreatedAt, &g.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan grade: %w", err)
		}
		grades = append(grades, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate grades: %w", err)
	}

	return grades, nil
}

// Delete removes a grade by ID.
// schoolID is required for ownership verification.
// Because categories and books have ON DELETE CASCADE from grades,
// deleting a grade removes all its categories and books automatically.
func (r *GradeRepository) Delete(ctx context.Context, id, schoolID string) error {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM grades WHERE id = $1 AND school_id = $2`, id, schoolID,
	)
	if err != nil {
		return fmt.Errorf("delete grade: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete grade rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}

	return nil
}

// RecordFailedLogin increments the failed login counter and locks the
// account atomically once the threshold is reached.
func (r *GradeRepository) RecordFailedLogin(ctx context.Context, id string, threshold int, lockoutDur time.Duration) error {
	query := `
		UPDATE grades
		SET
			login_attempts = login_attempts + 1,
			locked_until = CASE
				WHEN login_attempts + 1 >= $2 THEN now() + $3::interval
				ELSE locked_until
			END,
			updated_at = now()
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, id, threshold, lockoutDur.String())
	if err != nil {
		return fmt.Errorf("record grade failed login: %w", err)
	}

	return nil
}

// RecordSuccessfulLogin resets the brute force counters and records
// the login timestamp.
func (r *GradeRepository) RecordSuccessfulLogin(ctx context.Context, id string) error {
	query := `
		UPDATE grades
		SET
			login_attempts = 0,
			locked_until   = NULL,
			last_login_at  = now(),
			updated_at     = now()
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("record grade successful login: %w", err)
	}

	return nil
}