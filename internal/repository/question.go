package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
)

// QuestionRepository handles all database operations for the questions table.
type QuestionRepository struct {
	db *sql.DB
}

// NewQuestionRepository creates a QuestionRepository with the shared DB pool.
func NewQuestionRepository(db *sql.DB) *QuestionRepository {
	return &QuestionRepository{db: db}
}

// BulkCreate inserts all questions for a book in a single transaction.
// Called by the processor after Groq generates questions — either all
// questions are saved or none are, preventing partial quiz states.
func (r *QuestionRepository) BulkCreate(ctx context.Context, questions []*models.Question) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin bulk create: %w", err)
	}

	query := `
		INSERT INTO questions
			(book_id, grade_id, question_text, options, correct_index, order_index)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`

	for _, q := range questions {
		// Serialise the options slice to JSONB before inserting.
		optionsJSON, err := json.Marshal(q.Options)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("marshal options: %w", err)
		}

		err = tx.QueryRowContext(ctx, query,
			q.BookID,
			q.GradeID,
			q.QuestionText,
			optionsJSON,
			q.CorrectIndex,
			q.OrderIndex,
		).Scan(&q.ID)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("insert question %d: %w", q.OrderIndex, err)
		}
	}

	return tx.Commit()
}

// GetByBook fetches all questions for a book ordered by order_index.
// Returns questions without correct_index — that is stripped in the
// service layer before sending to the student. The repo returns the
// full model; access control is the service's responsibility.
func (r *QuestionRepository) GetByBook(ctx context.Context, bookID string) ([]*models.Question, error) {
	query := `
		SELECT id, book_id, grade_id, question_text, options,
		       correct_index, question_audio_path, audio_status,
		       order_index, created_at
		FROM questions
		WHERE book_id = $1
		ORDER BY order_index ASC`

	rows, err := r.db.QueryContext(ctx, query, bookID)
	if err != nil {
		return nil, fmt.Errorf("get questions by book: %w", err)
	}
	defer rows.Close()

	questions := []*models.Question{}
	for rows.Next() {
		q := &models.Question{}
		var optionsJSON []byte

		if err := rows.Scan(
			&q.ID, &q.BookID, &q.GradeID, &q.QuestionText,
			&optionsJSON, &q.CorrectIndex, &q.QuestionAudioPath,
			&q.AudioStatus, &q.OrderIndex, &q.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan question: %w", err)
		}

		// Deserialise JSONB options back into a string slice.
		if err := json.Unmarshal(optionsJSON, &q.Options); err != nil {
			return nil, fmt.Errorf("unmarshal options: %w", err)
		}

		questions = append(questions, q)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate questions: %w", err)
	}

	return questions, nil
}

// UpdateAudioPath sets the question_audio_path and marks audio_status = ready
// after the processor has synthesised the question's audio file.
func (r *QuestionRepository) UpdateAudioPath(ctx context.Context, questionID, audioPath string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE questions
		 SET question_audio_path = $1,
		     audio_status        = 'ready'
		 WHERE id = $2`,
		audioPath, questionID,
	)
	if err != nil {
		return fmt.Errorf("update question audio path: %w", err)
	}
	return nil
}
