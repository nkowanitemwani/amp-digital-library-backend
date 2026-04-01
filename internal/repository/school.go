package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
)

// SchoolRepository handles all database operations for the schools table.
// No business logic lives here — only SQL. The service layer decides
// when and why to call these methods.
type SchoolRepository struct {
	// db is unexported — nothing outside this package can bypass the
	// repository and talk to the database directly.
	db *sql.DB
}

// NewSchoolRepository creates a SchoolRepository with the shared DB pool.
// Receiving *sql.DB as a parameter (rather than using a global) means
// this repository can be tested by passing in a test DB connection.
func NewSchoolRepository(db *sql.DB) *SchoolRepository {
	return &SchoolRepository{db: db}
}

// Create inserts a new school row and returns the created school.
// Password hashing is done by the service before calling this method —
// this repository only stores whatever hash it receives.
func (r *SchoolRepository) Create(ctx context.Context, school *models.School) (*models.School, error) {
	query := `
		INSERT INTO schools (name, email, password_hash, location)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, email, password_hash, location, is_active,
		          login_attempts, locked_until, last_login_at, created_at, updated_at`

	created := &models.School{}
	err := r.db.QueryRowContext(ctx, query,
		school.Name,
		school.Email,
		school.PasswordHash,
		school.Location,
	).Scan(
		&created.ID,
		&created.Name,
		&created.Email,
		&created.PasswordHash,
		&created.Location,
		&created.IsActive,
		&created.LoginAttempts,
		&created.LockedUntil,
		&created.LastLoginAt,
		&created.CreatedAt,
		&created.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create school: %w", err)
	}

	return created, nil
}

// GetByEmail fetches a school by email address.
// Used by the login flow — returns the full row including password_hash
// so the service can verify the password. CITEXT on the email column
// means this lookup is case-insensitive at the DB level.
func (r *SchoolRepository) GetByEmail(ctx context.Context, email string) (*models.School, error) {
	query := `
		SELECT id, name, email, password_hash, location, is_active,
		       login_attempts, locked_until, last_login_at, created_at, updated_at
		FROM schools
		WHERE email = $1`

	school := &models.School{}
	err := r.db.QueryRowContext(ctx, query, email).Scan(
		&school.ID,
		&school.Name,
		&school.Email,
		&school.PasswordHash,
		&school.Location,
		&school.IsActive,
		&school.LoginAttempts,
		&school.LockedUntil,
		&school.LastLoginAt,
		&school.CreatedAt,
		&school.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// Return a clear sentinel so callers can distinguish "not found"
		// from an actual database error without inspecting error strings.
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get school by email: %w", err)
	}

	return school, nil
}

// GetByID fetches a school by its primary key.
// Used to verify the school from a JWT still exists and is active.
func (r *SchoolRepository) GetByID(ctx context.Context, id string) (*models.School, error) {
	query := `
		SELECT id, name, email, password_hash, location, is_active,
		       login_attempts, locked_until, last_login_at, created_at, updated_at
		FROM schools
		WHERE id = $1`

	school := &models.School{}
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&school.ID,
		&school.Name,
		&school.Email,
		&school.PasswordHash,
		&school.Location,
		&school.IsActive,
		&school.LoginAttempts,
		&school.LockedUntil,
		&school.LastLoginAt,
		&school.CreatedAt,
		&school.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get school by id: %w", err)
	}

	return school, nil
}

// RecordFailedLogin increments the failed login counter and, once the
// threshold is reached, sets locked_until to lock the account temporarily.
// Doing this in a single UPDATE prevents a race condition where two
// concurrent requests both read attempts=4 and neither triggers the lockout.
//
// threshold  — number of failures before locking (e.g. 5)
// lockoutDur — how long to lock the account (e.g. 15 minutes)
func (r *SchoolRepository) RecordFailedLogin(ctx context.Context, id string, threshold int, lockoutDur time.Duration) error {
	query := `
		UPDATE schools
		SET
			login_attempts = login_attempts + 1,

			-- Lock the account only when this increment hits the threshold.
			-- CASE runs inside the same UPDATE so the read and write are atomic.
			locked_until = CASE
				WHEN login_attempts + 1 >= $2 THEN now() + $3::interval
				ELSE locked_until
			END,

			updated_at = now()
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, id, threshold, lockoutDur.String())
	if err != nil {
		return fmt.Errorf("record failed login: %w", err)
	}

	return nil
}

// RecordSuccessfulLogin resets the brute force counters and records
// the login timestamp. Called only after password verification passes.
func (r *SchoolRepository) RecordSuccessfulLogin(ctx context.Context, id string) error {
	query := `
		UPDATE schools
		SET
			login_attempts = 0,
			locked_until   = NULL,
			last_login_at  = now(),
			updated_at     = now()
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("record successful login: %w", err)
	}

	return nil
}