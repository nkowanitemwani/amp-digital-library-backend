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

// gradeIDForRequest resolves the gradeID for a request.
// Admin routes read gradeID from the request (body or URL param).
// Grade routes read gradeID from the JWT subject via context.
// This keeps grade ownership enforcement consistent across both roles.
func gradeIDForRequest(c *gin.Context, requestGradeID string) (string, bool) {
	if middleware.RoleFromContext(c) == models.RoleGrade {
		// Grade accounts — gradeID must come from the JWT, not the request.
		// This prevents a grade from accessing another grade's books by
		// supplying a different gradeID in the request body or URL.
		return middleware.GradeIDFromContext(c), true
	}

	// Admin accounts — gradeID comes from the request.
	if requestGradeID == "" {
		return "", false
	}
	return requestGradeID, true
}

// Upload handles POST /books.
// Admin only. gradeID and categoryID come from the multipart form fields.
func (h *BookHandler) Upload(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize)

	if err := c.Request.ParseMultipartForm(10 << 20); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "request too large or not a valid multipart form",
		})
		return
	}

	var req models.CreateBookRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: "missing file — include the PDF as a form field named 'file'",
		})
		return
	}
	defer file.Close()
	_ = header.Filename

	pdfData, err := storage.ReadAndValidatePDF(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	// gradeID comes from the form field — validated by the service to
	// belong to this school before any DB writes occur.
	book, err := h.bookService.Upload(c.Request.Context(), schoolID, req.GradeID, &req, pdfData)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrCategoryNotFound):
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: err.Error()})
		case errors.Is(err, service.ErrUnitNumberTaken):
			c.JSON(http.StatusConflict, models.ErrorResponse{Error: err.Error()})
		case errors.Is(err, service.ErrNotFound):
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "grade not found"})
		default:
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to upload book"})
		}
		return
	}

	// 202 Accepted — book is queued for audio processing.
	c.JSON(http.StatusAccepted, book)
}

// GetByID handles GET /books/:id.
// Used by both admins and grade accounts.
// Grade accounts can only fetch books belonging to their own grade.
func (h *BookHandler) GetByID(c *gin.Context) {
	bookID := c.Param("id")
	if bookID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "book id is required"})
		return
	}

	// Resolve gradeID — from JWT for grade accounts, from query param for admins.
	gradeID, ok := gradeIDForRequest(c, c.Query("grade_id"))
	if !ok {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "grade_id query param is required"})
		return
	}

	book, err := h.bookService.GetByID(c.Request.Context(), gradeID, bookID)
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
// Grade accounts can only fetch books from their own grade's categories.
func (h *BookHandler) GetByCategory(c *gin.Context) {
	categoryID := c.Param("id")
	if categoryID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "category id is required"})
		return
	}

	gradeID, ok := gradeIDForRequest(c, c.Query("grade_id"))
	if !ok {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "grade_id query param is required"})
		return
	}

	books, err := h.bookService.GetByCategory(c.Request.Context(), gradeID, categoryID)
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
// Admin only. gradeID comes from the query param ?grade_id=
func (h *BookHandler) Delete(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)
	bookID   := c.Param("id")
	gradeID  := c.Query("grade_id")

	if bookID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "book id is required"})
		return
	}
	if gradeID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "grade_id query param is required"})
		return
	}

	err := h.bookService.Delete(c.Request.Context(), schoolID, gradeID, bookID)
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