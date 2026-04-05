package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/repository"
	"github.com/nkowanitemwani/amp-digital-library-backend/storage"
)

// =============================================================
// BOOK SERVICE
// Business rules for uploading and managing books within grades.
// Books are scoped to a grade — a grade can only see and manage
// its own books.
// =============================================================

var (
	// ErrUnitNumberTaken is returned when the unit number already exists
	// within this category.
	ErrUnitNumberTaken = errors.New("a book with this unit number already exists in this category")

	// ErrCategoryNotFound is returned when the category does not exist
	// or does not belong to the requesting grade.
	ErrCategoryNotFound = errors.New("category not found")

	// ErrBookNotReady is returned when audio is requested for a book
	// that has not finished processing yet.
	ErrBookNotReady = errors.New("audio is not ready yet — this book is still being processed")

	// ErrBookFailed is returned when audio is requested for a book
	// whose processing failed. The admin should re-upload.
	ErrBookFailed = errors.New("book processing failed — please re-upload the file")
)

// BookService handles book uploads, retrieval and deletion.
type BookService struct {
	bookRepo     *repository.BookRepository
	categoryRepo *repository.CategoryRepository
	gradeRepo    *repository.GradeRepository
	auditRepo    *repository.AuditRepository
	store        storage.Storage
}

// NewBookService creates a BookService with its dependencies.
func NewBookService(
	bookRepo *repository.BookRepository,
	categoryRepo *repository.CategoryRepository,
	gradeRepo *repository.GradeRepository,
	auditRepo *repository.AuditRepository,
	store storage.Storage,
) *BookService {
	return &BookService{
		bookRepo:     bookRepo,
		categoryRepo: categoryRepo,
		gradeRepo:    gradeRepo,
		auditRepo:    auditRepo,
		store:        store,
	}
}

// Upload saves a PDF and creates a book row with status 'processing'.
// The processor picks up the row from the DB queue and generates audio
// asynchronously — the response is returned immediately.
//
// schoolID and gradeID come from the validated JWT — never from the request body.
func (s *BookService) Upload(ctx context.Context, schoolID, gradeID string, req *models.CreateBookRequest, pdfData []byte) (*models.BookResponse, error) {
	// Verify the grade belongs to this school.
	if _, err := s.gradeRepo.GetByID(ctx, gradeID, schoolID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("verify grade: %w", err)
	}

	// Verify the category belongs to this grade.
	// The composite FK in the DB enforces this structurally, but we
	// check here too to return a meaningful error before hitting the DB.
	if _, err := s.categoryRepo.GetByID(ctx, req.CategoryID, gradeID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrCategoryNotFound
		}
		return nil, fmt.Errorf("verify category: %w", err)
	}

	// Create the book row first to get the real book ID, then save
	// the PDF using that ID as the storage key.
	book, err := s.bookRepo.Create(ctx, &models.Book{
		SchoolID:   schoolID,
		GradeID:    gradeID,
		CategoryID: req.CategoryID,
		Title:      req.Title,
		Author:     req.Author,
		UnitNumber: req.UnitNumber,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUnitNumberTaken
		}
		return nil, fmt.Errorf("create book record: %w", err)
	}

	// Save the PDF using the book's UUID as the key.
	pdfKey := storage.PDFKey(book.ID)
	if err := s.store.Save(ctx, pdfKey, pdfData, "application/pdf"); err != nil {
		// PDF save failed — mark the book as failed so it does not sit
		// in 'pending' forever, then surface the error to the admin.
		s.bookRepo.UpdateStatusFailed(ctx, book.ID)
		return nil, fmt.Errorf("save pdf: %w", err)
	}

	// PDF saved — set pdf_path and atomically flip status to 'processing'.
	// The processor will now pick this book up on its next poll cycle.
	if err := s.bookRepo.UpdatePDFPath(ctx, book.ID, pdfKey, book.Version); err != nil {
		return nil, fmt.Errorf("update pdf path: %w", err)
	}

	// Refresh to get the updated version before building the response.
	book, err = s.bookRepo.GetByID(ctx, book.ID, gradeID)
	if err != nil {
		return nil, fmt.Errorf("fetch created book: %w", err)
	}

	s.auditRepo.Log(ctx, &models.AuditEntry{
		SchoolID: &schoolID,
		Action:   "book.uploaded",
		Entity:   "book",
		EntityID: &book.ID,
		Metadata: map[string]string{
			"title":       book.Title,
			"unit_number": fmt.Sprintf("%d", book.UnitNumber),
			"grade_id":    gradeID,
		},
	})

	return toBookResponse(book, s.store), nil
}

// GetByID returns a single book.
// gradeID from the JWT ensures a grade can only fetch their own books.
func (s *BookService) GetByID(ctx context.Context, gradeID, bookID string) (*models.BookResponse, error) {
	book, err := s.bookRepo.GetByID(ctx, bookID, gradeID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get book: %w", err)
	}

	return toBookResponse(book, s.store), nil
}

// GetByCategory returns all books in a category ordered by unit number.
func (s *BookService) GetByCategory(ctx context.Context, gradeID, categoryID string) ([]*models.BookResponse, error) {
	// Verify the category belongs to this grade.
	if _, err := s.categoryRepo.GetByID(ctx, categoryID, gradeID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrCategoryNotFound
		}
		return nil, fmt.Errorf("verify category: %w", err)
	}

	books, err := s.bookRepo.GetAllByCategory(ctx, categoryID, gradeID)
	if err != nil {
		return nil, fmt.Errorf("get books by category: %w", err)
	}

	responses := make([]*models.BookResponse, len(books))
	for i, book := range books {
		responses[i] = toBookResponse(book, s.store)
	}

	return responses, nil
}

// Delete removes a book and its associated files from storage.
func (s *BookService) Delete(ctx context.Context, schoolID, gradeID, bookID string) error {
	book, err := s.bookRepo.GetByID(ctx, bookID, gradeID)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get book for delete: %w", err)
	}

	// Delete the DB row first — if file cleanup fails we lose the
	// orphaned files but the book is cleanly gone from the library.
	if err := s.bookRepo.Delete(ctx, bookID, gradeID); err != nil {
		return fmt.Errorf("delete book record: %w", err)
	}

	// Best-effort file cleanup.
	if book.PDFPath != nil {
		if err := s.store.Delete(ctx, *book.PDFPath); err != nil {
			fmt.Printf("warning: could not delete pdf %s: %v\n", *book.PDFPath, err)
		}
	}
	if book.AudioPath != nil {
		if err := s.store.Delete(ctx, *book.AudioPath); err != nil {
			fmt.Printf("warning: could not delete audio %s: %v\n", *book.AudioPath, err)
		}
	}

	s.auditRepo.Log(ctx, &models.AuditEntry{
		SchoolID: &schoolID,
		Action:   "book.deleted",
		Entity:   "book",
		EntityID: &bookID,
	})

	return nil
}

// toBookResponse converts a Book to a BookResponse.
// AudioURL is resolved via SignedURL so the client receives a
// time-limited URL they can stream directly — works for both
// local disk (plain URL) and S3 (presigned URL).
// A background context is used here since this is a read-only
// operation and we do not want a request timeout to break URL generation.
func toBookResponse(b *models.Book, store storage.Storage) *models.BookResponse {
	resp := &models.BookResponse{
		ID:         b.ID,
		GradeID:    b.GradeID,
		CategoryID: b.CategoryID,
		Title:      b.Title,
		Author:     b.Author,
		UnitNumber: b.UnitNumber,
		Status:     b.Status,
		CreatedAt:  b.CreatedAt,
	}

	// Only resolve the audio URL when the book is fully processed.
	// Pending and processing books have no audio yet.
	if b.Status == models.BookStatusReady && b.AudioPath != nil {
		url, err := store.SignedURL(context.Background(), *b.AudioPath, storage.AudioSignedURLTTL)
		if err == nil {
			resp.AudioURL = url
		}
	}

	return resp
}