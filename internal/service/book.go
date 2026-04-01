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
// Business rules for uploading and managing books.
// The processor (processor.go) handles the async PDF → audio pipeline.
// This service handles the synchronous part: validate, store PDF,
// create the DB row, then hand off to the processor via the DB queue.
// =============================================================

// Sentinel errors specific to books.
var (
	// ErrUnitNumberTaken is returned when a school tries to add a book
	// with a unit number that already exists in that category.
	ErrUnitNumberTaken = errors.New("a book with this unit number already exists in the category")

	// ErrCategoryNotFound is returned when the specified category does
	// not exist or does not belong to the requesting school.
	ErrCategoryNotFound = errors.New("category not found")

	// ErrBookNotReady is returned when audio is requested for a book
	// that has not finished processing yet.
	ErrBookNotReady = errors.New("audio is not ready yet — book is still processing")

	// ErrBookFailed is returned when audio is requested for a book
	// whose processing failed. The admin should re-upload the book.
	ErrBookFailed = errors.New("book processing failed — please re-upload the file")
)

// BookService handles book uploads, retrieval and deletion.
type BookService struct {
	bookRepo     *repository.BookRepository
	categoryRepo *repository.CategoryRepository
	auditRepo    *repository.AuditRepository
	store        storage.Storage
}

// NewBookService creates a BookService with its dependencies.
func NewBookService(
	bookRepo *repository.BookRepository,
	categoryRepo *repository.CategoryRepository,
	auditRepo *repository.AuditRepository,
	store storage.Storage,
) *BookService {
	return &BookService{
		bookRepo:     bookRepo,
		categoryRepo: categoryRepo,
		auditRepo:    auditRepo,
		store:        store,
	}
}

// Upload saves a PDF and creates a book row with status 'processing'.
// The processor picks up the row from the DB queue and generates audio
// asynchronously — the admin does not wait for that to complete here.
//
// schoolID comes from the validated JWT — never from the request body.
// pdfData is the raw bytes of the validated PDF file.
func (s *BookService) Upload(ctx context.Context, schoolID string, req *models.CreateBookRequest, pdfData []byte) (*models.BookResponse, error) {
	// Verify the category exists and belongs to this school.
	// This is the application-level ownership check — the DB composite FK
	// is the structural guarantee underneath it.
	_, err := s.categoryRepo.GetByID(ctx, req.CategoryID, schoolID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrCategoryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("verify category: %w", err)
	}

	// Build the storage key before creating the DB row so we have a key
	// to store. We use a temporary placeholder ID here — the real book ID
	// comes back from the INSERT RETURNING below.
	// To avoid this chicken-and-egg, we create the book row first (with no
	// pdf_path) and update it after saving the file.
	book, err := s.bookRepo.Create(ctx, &models.Book{
		SchoolID:   schoolID,
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

	// Now we have the real book ID — build the storage key and save the PDF.
	pdfKey := storage.PDFKey(book.ID)
	if err := s.store.Save(ctx, pdfKey, pdfData, "application/pdf"); err != nil {
		// The book row exists but the file did not save. Mark the book as
		// failed so it does not sit in 'processing' forever, then surface
		// the error to the admin.
		s.bookRepo.UpdateStatusFailed(ctx, book.ID)
		return nil, fmt.Errorf("save pdf: %w", err)
	}

	// Update the book row with the PDF path now that the file is saved.
	// The processor uses this path to fetch the PDF for text extraction.
	if err := s.bookRepo.UpdatePDFPath(ctx, book.ID, pdfKey, book.Version); err != nil {
		return nil, fmt.Errorf("update pdf path: %w", err)
	}

	// Refresh the book to get the updated fields before building the response.
	book, err = s.bookRepo.GetByID(ctx, book.ID, schoolID)
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
		},
	})

	return toBookResponse(book, s.store), nil
}

// GetByID returns a single book.
// schoolID from the JWT ensures a school can only fetch their own books.
func (s *BookService) GetByID(ctx context.Context, schoolID, bookID string) (*models.BookResponse, error) {
	book, err := s.bookRepo.GetByID(ctx, bookID, schoolID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get book: %w", err)
	}

	return toBookResponse(book, s.store), nil
}

// GetByCategory returns all books in a category ordered by unit number.
// Verifies the category belongs to the school before querying books.
func (s *BookService) GetByCategory(ctx context.Context, schoolID, categoryID string) ([]*models.BookResponse, error) {
	// Ownership check — ensures the category belongs to this school.
	_, err := s.categoryRepo.GetByID(ctx, categoryID, schoolID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrCategoryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("verify category: %w", err)
	}

	books, err := s.bookRepo.GetAllByCategory(ctx, categoryID, schoolID)
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
// schoolID from the JWT prevents cross-school deletion.
func (s *BookService) Delete(ctx context.Context, schoolID, bookID string) error {
	// Fetch first so we have the storage keys to clean up.
	book, err := s.bookRepo.GetByID(ctx, bookID, schoolID)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get book for delete: %w", err)
	}

	// Delete the DB row first. If storage cleanup fails after this we lose
	// the file references but the book is gone from the library — acceptable.
	// The alternative (delete files first) risks orphaned DB rows if the
	// DB delete fails, which is harder to recover from.
	if err := s.bookRepo.Delete(ctx, bookID, schoolID); err != nil {
		return fmt.Errorf("delete book record: %w", err)
	}

	// Best-effort file cleanup — log failures but do not surface them,
	// since the book is already removed from the library.
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

// =============================================================
// RESPONSE BUILDER
// AudioURL is only set when the book is ready — the client receives
// an empty string until then, which omitempty drops from the JSON.
// =============================================================

func toBookResponse(b *models.Book, store storage.Storage) *models.BookResponse {
	resp := &models.BookResponse{
		ID:         b.ID,
		CategoryID: b.CategoryID,
		Title:      b.Title,
		Author:     b.Author,
		UnitNumber: b.UnitNumber,
		Status:     b.Status,
		CreatedAt:  b.CreatedAt,
	}

	// Only resolve the audio URL when the book is fully processed.
	// Returning a URL for an unready book would point to a file that
	// does not exist yet.
	if b.Status == models.BookStatusReady && b.AudioPath != nil {
		resp.AudioURL = store.URL(*b.AudioPath)
	}

	return resp
}