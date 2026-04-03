package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
)

// BookRepository handles all database operations for the books table.
// Books are scoped to a grade — every query includes grade_id so a grade
// can never access another grade's books.
type BookRepository struct {
	db *sql.DB
}

// NewBookRepository creates a BookRepository with the shared DB pool.
func NewBookRepository(db *sql.DB) *BookRepository {
	return &BookRepository{db: db}
}

// Create inserts a new book row with status = 'processing' and returns it.
// pdf_path is set here. audio_path is populated later by UpdateAudioReady
// once background processing completes.
func (r *BookRepository) Create(ctx context.Context, book *models.Book) (*models.Book, error) {
	query := `
		INSERT INTO books (school_id, grade_id, category_id, title, author, unit_number, pdf_path, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'processing')
		RETURNING id, school_id, grade_id, category_id, title, author, unit_number,
		          pdf_path, audio_path, status, version, created_at, updated_at`

	created := &models.Book{}
	err := r.db.QueryRowContext(ctx, query,
		book.SchoolID,
		book.GradeID,
		book.CategoryID,
		book.Title,
		book.Author,
		book.UnitNumber,
		book.PDFPath,
	).Scan(
		&created.ID, &created.SchoolID, &created.GradeID, &created.CategoryID,
		&created.Title, &created.Author, &created.UnitNumber,
		&created.PDFPath, &created.AudioPath, &created.Status,
		&created.Version, &created.CreatedAt, &created.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create book: %w", err)
	}

	return created, nil
}

// GetByID fetches a single book by its primary key.
// gradeID is required — prevents a grade from accessing another grade's books.
func (r *BookRepository) GetByID(ctx context.Context, id, gradeID string) (*models.Book, error) {
	query := `
		SELECT id, school_id, grade_id, category_id, title, author, unit_number,
		       pdf_path, audio_path, status, version, created_at, updated_at
		FROM books
		WHERE id = $1 AND grade_id = $2`

	book := &models.Book{}
	err := r.db.QueryRowContext(ctx, query, id, gradeID).Scan(
		&book.ID, &book.SchoolID, &book.GradeID, &book.CategoryID,
		&book.Title, &book.Author, &book.UnitNumber,
		&book.PDFPath, &book.AudioPath, &book.Status,
		&book.Version, &book.CreatedAt, &book.UpdatedAt,
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
// gradeID is included as a second ownership check alongside the composite
// FK already enforced at the DB level.
func (r *BookRepository) GetAllByCategory(ctx context.Context, categoryID, gradeID string) ([]*models.Book, error) {
	query := `
		SELECT id, school_id, grade_id, category_id, title, author, unit_number,
		       pdf_path, audio_path, status, version, created_at, updated_at
		FROM books
		WHERE category_id = $1 AND grade_id = $2
		ORDER BY unit_number ASC`

	rows, err := r.db.QueryContext(ctx, query, categoryID, gradeID)
	if err != nil {
		return nil, fmt.Errorf("get books by category: %w", err)
	}
	defer rows.Close()

	books := []*models.Book{}
	for rows.Next() {
		book := &models.Book{}
		if err := rows.Scan(
			&book.ID, &book.SchoolID, &book.GradeID, &book.CategoryID,
			&book.Title, &book.Author, &book.UnitNumber,
			&book.PDFPath, &book.AudioPath, &book.Status,
			&book.Version, &book.CreatedAt, &book.UpdatedAt,
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
// gradeID is required for ownership verification.
func (r *BookRepository) Delete(ctx context.Context, id, gradeID string) error {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM books WHERE id = $1 AND grade_id = $2`, id, gradeID,
	)
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

// ClaimNextPending atomically claims one pending book for the processor.
// Uses SELECT FOR UPDATE SKIP LOCKED — see book_repo original for full
// explanation of this pattern.
func (r *BookRepository) ClaimNextPending(ctx context.Context) (*models.Book, *sql.Tx, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin claim transaction: %w", err)
	}

	query := `
		SELECT id, school_id, grade_id, category_id, title, author, unit_number,
		       pdf_path, audio_path, status, version, created_at, updated_at
		FROM books
		WHERE status = 'processing'
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`

	book := &models.Book{}
	err = tx.QueryRowContext(ctx, query).Scan(
		&book.ID, &book.SchoolID, &book.GradeID, &book.CategoryID,
		&book.Title, &book.Author, &book.UnitNumber,
		&book.PDFPath, &book.AudioPath, &book.Status,
		&book.Version, &book.CreatedAt, &book.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		return nil, nil, nil
	}
	if err != nil {
		tx.Rollback()
		return nil, nil, fmt.Errorf("claim next pending: %w", err)
	}

	return book, tx, nil
}

// UpdatePDFPath sets the pdf_path after the file has been saved to storage.
func (r *BookRepository) UpdatePDFPath(ctx context.Context, id, pdfPath string, version int) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE books SET pdf_path = $1, version = version + 1 WHERE id = $2 AND version = $3`,
		pdfPath, id, version,
	)
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

// UpdateAudioReady marks a book as ready and records its audio path.
// Must be called within the transaction returned by ClaimNextPending.
// Uses optimistic locking — zero rows affected means a conflict occurred.
func (r *BookRepository) UpdateAudioReady(ctx context.Context, tx *sql.Tx, id, audioPath string, version int) error {
	result, err := tx.ExecContext(ctx,
		`UPDATE books SET audio_path = $1, status = 'ready', version = version + 1
		 WHERE id = $2 AND version = $3`,
		audioPath, id, version,
	)
	if err != nil {
		return fmt.Errorf("update audio ready: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update audio ready rows affected: %w", err)
	}
	if rows == 0 {
		return ErrConflict
	}

	return nil
}

// UpdateStatusFailed marks a book as failed.
// Called by the processor when any processing step fails.
func (r *BookRepository) UpdateStatusFailed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE books SET status = 'failed', version = version + 1 WHERE id = $1`, id,
	)
	if err != nil {
		return fmt.Errorf("update status failed: %w", err)
	}

	return nil
}