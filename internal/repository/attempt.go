package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
)

// AttemptRepository handles all database operations for the quiz_attempts table.
// Attempts are append-only — they are never updated or deleted, giving
// teachers a full history of how a grade has performed over time.
type AttemptRepository struct {
	db *sql.DB
}

// NewAttemptRepository creates an AttemptRepository with the shared DB pool.
func NewAttemptRepository(db *sql.DB) *AttemptRepository {
	return &AttemptRepository{db: db}
}

// Create records a completed quiz attempt and returns the saved row.
func (r *AttemptRepository) Create(ctx context.Context, attempt *models.QuizAttempt) (*models.QuizAttempt, error) {
	// Serialise the answers slice to JSONB before inserting.
	answersJSON, err := json.Marshal(attempt.Answers)
	if err != nil {
		return nil, fmt.Errorf("marshal answers: %w", err)
	}

	query := `
		INSERT INTO quiz_attempts (book_id, grade_id, score, total, answers)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, book_id, grade_id, score, total, answers, completed_at`

	saved := &models.QuizAttempt{}
	var savedAnswersJSON []byte

	err = r.db.QueryRowContext(ctx, query,
		attempt.BookID,
		attempt.GradeID,
		attempt.Score,
		attempt.Total,
		answersJSON,
	).Scan(
		&saved.ID, &saved.BookID, &saved.GradeID,
		&saved.Score, &saved.Total,
		&savedAnswersJSON, &saved.CompletedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create attempt: %w", err)
	}

	if err := json.Unmarshal(savedAnswersJSON, &saved.Answers); err != nil {
		return nil, fmt.Errorf("unmarshal saved answers: %w", err)
	}

	return saved, nil
}

// GetByGrade fetches all attempts for every book belonging to a grade,
// ordered most recent first. Used by the teacher progress dashboard.
func (r *AttemptRepository) GetByGrade(ctx context.Context, gradeID string) ([]*models.QuizAttempt, error) {
	query := `
		SELECT id, book_id, grade_id, score, total, answers, completed_at
		FROM quiz_attempts
		WHERE grade_id = $1
		ORDER BY completed_at DESC`

	rows, err := r.db.QueryContext(ctx, query, gradeID)
	if err != nil {
		return nil, fmt.Errorf("get attempts by grade: %w", err)
	}
	defer rows.Close()

	attempts := []*models.QuizAttempt{}
	for rows.Next() {
		a := &models.QuizAttempt{}
		var answersJSON []byte

		if err := rows.Scan(
			&a.ID, &a.BookID, &a.GradeID,
			&a.Score, &a.Total,
			&answersJSON, &a.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}

		if err := json.Unmarshal(answersJSON, &a.Answers); err != nil {
			return nil, fmt.Errorf("unmarshal answers: %w", err)
		}

		attempts = append(attempts, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate attempts: %w", err)
	}

	return attempts, nil
}

// GetByBook fetches all attempts for a specific book within a grade.
// Used to show per-book quiz history.
func (r *AttemptRepository) GetByBook(ctx context.Context, bookID, gradeID string) ([]*models.QuizAttempt, error) {
	query := `
		SELECT id, book_id, grade_id, score, total, answers, completed_at
		FROM quiz_attempts
		WHERE book_id = $1 AND grade_id = $2
		ORDER BY completed_at DESC`

	rows, err := r.db.QueryContext(ctx, query, bookID, gradeID)
	if err != nil {
		return nil, fmt.Errorf("get attempts by book: %w", err)
	}
	defer rows.Close()

	attempts := []*models.QuizAttempt{}
	for rows.Next() {
		a := &models.QuizAttempt{}
		var answersJSON []byte

		if err := rows.Scan(
			&a.ID, &a.BookID, &a.GradeID,
			&a.Score, &a.Total,
			&answersJSON, &a.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}

		if err := json.Unmarshal(answersJSON, &a.Answers); err != nil {
			return nil, fmt.Errorf("unmarshal answers: %w", err)
		}

		attempts = append(attempts, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate attempts: %w", err)
	}

	return attempts, nil
}
