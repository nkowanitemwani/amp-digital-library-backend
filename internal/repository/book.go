package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
)

// BookRepository handles all database operations for the books table.
// This is also where the processor job queue is implemented using
// SELECT FOR UPDATE SKIP LOCKED — see ClaimNextPending for details.
type BookRepository struct {
	db *sql.DB
}

// NewBookRepository creates a BookRepository with the shared DB pool.
func NewBookRepository(db *sql.DB) *BookRepository {
	return &BookRepository{db: db}
}

// Create inserts a new book row with status = 'processing' and returns it.
// pdf_path is set here. audio_path is set later by UpdateAudioReady
// once background processing completes.
func (r *BookRepository) Create(ctx context.Context, book *models.Book) (*models.Book, error) {
	query := `
		INSERT INTO books (school_id, category_id, title, author, unit_number, pdf_path, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'processing')
		RETURNING id, school_id, category_id, title, author, unit_number,
		          pdf_path, audio_path, status, version, created_at, updated_at`

	created := &models.Book{}
	err := r.db.QueryRowContext(ctx, query,
		book.SchoolID,
		book.CategoryID,
		book.Title,
		book.Author,
		book.UnitNumber,
		book.PDFPath,
	).Scan(
		&created.ID,
		&created.SchoolID,
		&created.CategoryID,
		&created.Title,
		&created.Author,
		&created.UnitNumber,
		&created.PDFPath,
		&created.AudioPath,
		&created.Status,
		&created.Version,
		&created.CreatedAt,
		&created.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create book: %w", err)
	}

	return created, nil
}

// GetByID fetches a single book by its primary key.
// schoolID is required to prevent a school from accessing another
// school's books even if they know the book's UUID.
func (r *BookRepository) GetByID(ctx context.Context, id, schoolID string) (*models.Book, error) {
	query := `
		SELECT id, school_id, category_id, title, author, unit_number,
		       pdf_path, audio_path, status, version, created_at, updated_at
		FROM books
		WHERE id = $1 AND school_id = $2`

	book := &models.Book{}
	err := r.db.QueryRowContext(ctx, query, id, schoolID).Scan(
		&book.ID,
		&book.SchoolID,
		&book.CategoryID,
		&book.Title,
		&book.Author,
		&book.UnitNumber,
		&book.PDFPath,
		&book.AudioPath,
		&book.Status,
		&book.Version,
		&book.CreatedAt,
		&book.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get book by id: %w", err)
	}

	return book, nil
}

// GetAllByCategory returns all books in a category ordered by unit_number.
// schoolID is included in the WHERE clause as a second ownership check —
// the composite FK in the schema already prevents cross-school books at
// the DB level, but this makes the intent explicit in the query itself.
func (r *BookRepository) GetAllByCategory(ctx context.Context, categoryID, schoolID string) ([]*models.Book, error) {
	query := `
		SELECT id, school_id, category_id, title, author, unit_number,
		       pdf_path, audio_path, status, version, created_at, updated_at
		FROM books
		WHERE category_id = $1 AND school_id = $2
		ORDER BY unit_number ASC`

	rows, err := r.db.QueryContext(ctx, query, categoryID, schoolID)
	if err != nil {
		return nil, fmt.Errorf("get books by category: %w", err)
	}
	defer rows.Close()

	books := []*models.Book{}
	for rows.Next() {
		book := &models.Book{}
		if err := rows.Scan(
			&book.ID,
			&book.SchoolID,
			&book.CategoryID,
			&book.Title,
			&book.Author,
			&book.UnitNumber,
			&book.PDFPath,
			&book.AudioPath,
			&book.Status,
			&book.Version,
			&book.CreatedAt,
			&book.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan book: %w", err)
		}
		books = append(books, book)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate books: %w", err)
	}

	return books, nil
}

// Delete removes a book by ID.
// schoolID is required for ownership verification.
func (r *BookRepository) Delete(ctx context.Context, id, schoolID string) error {
	query := `
		DELETE FROM books
		WHERE id = $1 AND school_id = $2`

	result, err := r.db.ExecContext(ctx, query, id, schoolID)
	if err != nil {
		return fmt.Errorf("delete book: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete book rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}

	return nil
}

// ClaimNextPending is the processor job queue.
//
// It uses SELECT FOR UPDATE SKIP LOCKED inside a transaction to
// atomically find and lock one unprocessed book. This is the correct
// pattern for a concurrent job queue without an external queue service:
//
//   - FOR UPDATE locks the selected row so no other worker can claim it.
//   - SKIP LOCKED means a second worker skips that row rather than waiting,
//     so two goroutines never process the same book simultaneously.
//   - The transaction is returned to the caller — it must be committed
//     or rolled back after processing completes.
//
// Returns (nil, nil, nil) when there are no pending books —
// the caller should sleep and retry rather than treating this as an error.
func (r *BookRepository) ClaimNextPending(ctx context.Context) (*models.Book, *sql.Tx, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin claim transaction: %w", err)
	}

	query := `
		SELECT id, school_id, category_id, title, author, unit_number,
		       pdf_path, audio_path, status, version, created_at, updated_at
		FROM books
		WHERE status = 'processing'
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`

	book := &models.Book{}
	err = tx.QueryRowContext(ctx, query).Scan(
		&book.ID,
		&book.SchoolID,
		&book.CategoryID,
		&book.Title,
		&book.Author,
		&book.UnitNumber,
		&book.PDFPath,
		&book.AudioPath,
		&book.Status,
		&book.Version,
		&book.CreatedAt,
		&book.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// No pending books — roll back the empty transaction and signal
		// the caller to sleep rather than spin.
		tx.Rollback()
		return nil, nil, nil
	}
	if err != nil {
		tx.Rollback()
		return nil, nil, fmt.Errorf("claim next pending: %w", err)
	}

	// Return the open transaction — the caller commits it after uploading
	// the audio and calling UpdateAudioReady.
	return book, tx, nil
}

// UpdateAudioReady marks a book as ready and records its audio path.
// Must be called within the transaction returned by ClaimNextPending.
//
// Optimistic locking: the WHERE clause includes version = $3.
// If zero rows are affected, a concurrent update already changed this
// row — the caller should treat this as a conflict and not commit.
func (r *BookRepository) UpdateAudioReady(ctx context.Context, tx *sql.Tx, id, audioPath string, version int) error {
	query := `
		UPDATE books
		SET
			audio_path = $1,
			status     = 'ready',

			-- Increment version so any concurrent update that also read
			-- the old version will detect the conflict via zero rows affected.
			version    = version + 1
		WHERE id = $2 AND version = $3`

	result, err := tx.ExecContext(ctx, query, audioPath, id, version)
	if err != nil {
		return fmt.Errorf("update audio ready: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update audio ready rows affected: %w", err)
	}

	// Zero rows means the version didn't match — optimistic lock conflict.
	if rows == 0 {
		return ErrConflict
	}

	return nil
}

// UpdatePDFPath sets the pdf_path on a book after the file has been
// successfully saved to storage. Uses optimistic locking to guard
// against the unlikely case of a concurrent update on a brand new row.
func (r *BookRepository) UpdatePDFPath(ctx context.Context, id, pdfPath string, version int) error {
	query := `
		UPDATE books
		SET pdf_path = $1,
		    version  = version + 1
		WHERE id = $2 AND version = $3`

	result, err := r.db.ExecContext(ctx, query, pdfPath, id, version)
	if err != nil {
		return fmt.Errorf("update pdf path: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update pdf path rows affected: %w", err)
	}
	if rows == 0 {
		return ErrConflict
	}

	return nil
}

// UpdateStatusFailed marks a book as failed.
// Called by the processor when any step (text extraction, Polly, upload) fails.
// Uses a plain ExecContext rather than a transaction because failure
// updates are always safe to apply — there is no concurrent success path.
func (r *BookRepository) UpdateStatusFailed(ctx context.Context, id string) error {
	query := `
		UPDATE books
		SET status  = 'failed',
		    version = version + 1
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("update status failed: %w", err)
	}

	return nil
}