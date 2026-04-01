package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/middleware"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/service"
	"github.com/nkowanitemwani/amp-digital-library-backend/storage"
)

// maxUploadSize is the maximum PDF file size accepted by the upload endpoint.
// 50MB is generous for a textbook PDF. Enforced before reading the file into
// memory to prevent a large upload from exhausting the server's RAM.
const maxUploadSize = 50 << 20 // 50MB

// BookHandler handles HTTP requests for book management.
// The upload endpoint is multipart/form-data (file + fields).
// All other endpoints are standard JSON.
type BookHandler struct {
	bookService *service.BookService
}

// NewBookHandler creates a BookHandler with its dependency injected.
func NewBookHandler(bookService *service.BookService) *BookHandler {
	return &BookHandler{bookService: bookService}
}

// Upload handles POST /books.
// Accepts a multipart form with a PDF file and book metadata fields.
// The PDF is validated, saved to storage, and a book row is created
// with status 'processing'. The audio is generated asynchronously
// by the processor — the response is returned immediately.
func (h *BookHandler) Upload(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)

	// Cap the request body size before parsing the multipart form.
	// Without this, a client could send an arbitrarily large file and
	// exhaust server memory before we get a chance to reject it.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize)

	// ParseMultipartForm allocates a buffer for the form data.
	// 10MB in memory, remainder spilled to disk — reasonable for most PDFs.
	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "request too large or not a valid multipart form",
		})
		return
	}

	// Bind the non-file form fields (category_id, title, author, unit_number).
	var req models.CreateBookRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	// Retrieve the uploaded file from the "file" form field.
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "missing file — include the PDF as a form field named 'file'",
		})
		return
	}
	defer file.Close()

	// Log the filename for debugging — not used for storage (we use the book ID).
	_ = header.Filename

	// ReadAndValidatePDF reads all bytes and confirms the PDF magic bytes.
	// Rejects non-PDF files regardless of the declared Content-Type or
	// file extension — we check the actual content.
	pdfData, err := storage.ReadAndValidatePDF(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	book, err := h.bookService.Upload(c.Request.Context(), schoolID, &req, pdfData)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrCategoryNotFound):
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: err.Error()})
		case errors.Is(err, service.ErrUnitNumberTaken):
			c.JSON(http.StatusConflict, models.ErrorResponse{Error: err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to upload book"})
		}
		return
	}

	// 202 Accepted — the book was received and queued for processing.
	// The client should poll GET /books/:id to check when status = 'ready'.
	c.JSON(http.StatusAccepted, book)
}

// GetByID handles GET /books/:id.
// Returns a single book including its audio URL if processing is complete.
// Clients can poll this endpoint to check processing status.
func (h *BookHandler) GetByID(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)
	bookID := c.Param("id")

	if bookID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "book id is required"})
		return
	}

	book, err := h.bookService.GetByID(c.Request.Context(), schoolID, bookID)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "book not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to fetch book"})
		return
	}

	c.JSON(http.StatusOK, book)
}

// GetByCategory handles GET /categories/:id/books.
// Returns all books in a category ordered by unit number.
// This is the primary student-facing endpoint — a student selects a
// category and sees all units in listening order.
func (h *BookHandler) GetByCategory(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)
	categoryID := c.Param("id")

	if categoryID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "category id is required"})
		return
	}

	books, err := h.bookService.GetByCategory(c.Request.Context(), schoolID, categoryID)
	if err != nil {
		if errors.Is(err, service.ErrCategoryNotFound) {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "category not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to fetch books"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"books": books,
		"count": len(books),
	})
}

// Delete handles DELETE /books/:id.
// Removes a book and its associated files (PDF and audio) from storage.
// Returns 404 if the book does not exist or belongs to a different school.
func (h *BookHandler) Delete(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)
	bookID := c.Param("id")

	if bookID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "book id is required"})
		return
	}

	err := h.bookService.Delete(c.Request.Context(), schoolID, bookID)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "book not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to delete book"})
		return
	}

	c.JSON(http.StatusOK, models.MessageResponse{Message: "book deleted successfully"})
}