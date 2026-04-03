package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/middleware"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/service"
)

// CategoryHandler handles HTTP requests for category management.
// Categories are scoped to a grade — every endpoint requires a gradeID.
// Admins supply gradeID via the request body or URL param.
// Grade accounts read gradeID directly from their JWT context.
type CategoryHandler struct {
	categoryService *service.CategoryService
}

// NewCategoryHandler creates a CategoryHandler with its dependency injected.
func NewCategoryHandler(categoryService *service.CategoryService) *CategoryHandler {
	return &CategoryHandler{categoryService: categoryService}
}

// Create handles POST /categories.
// Admin only. gradeID comes from the request body (CreateCategoryRequest).
// schoolID comes from the admin's JWT — never from the request body.
func (h *CategoryHandler) Create(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)

	var req models.CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	// gradeID is inside the validated request body — the service verifies
	// it belongs to this school before creating the category.
	category, err := h.categoryService.Create(c.Request.Context(), schoolID, req.GradeID, &req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrCategoryNameTaken):
			c.JSON(http.StatusConflict, models.ErrorResponse{Error: err.Error()})
		case errors.Is(err, service.ErrNotFound):
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "grade not found"})
		default:
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to create category"})
		}
		return
	}

	c.JSON(http.StatusCreated, category)
}

// GetAll handles GET /grades/:id/categories.
// Used by both admins (grade id from URL param) and grade accounts
// (grade id from JWT subject). The gradeID source differs by role
// but the service call is identical.
func (h *CategoryHandler) GetAll(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)

	// gradeID comes from the URL param (:id on /grades/:id/categories).
	// For grade accounts this is the same value stored in their JWT,
	// but reading it from the URL keeps the handler uniform for both roles.
	gradeID := c.Param("id")
	if gradeID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "grade id is required"})
		return
	}

	// For grade-role requests: enforce that the gradeID in the URL matches
	// the grade in the JWT — a grade cannot browse another grade's categories.
	if middleware.RoleFromContext(c) == models.RoleGrade {
		if middleware.GradeIDFromContext(c) != gradeID {
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: "you can only access your own grade's categories"})
			return
		}
	}

	categories, err := h.categoryService.GetAll(c.Request.Context(), schoolID, gradeID)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "grade not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to fetch categories"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"categories": categories,
		"count":      len(categories),
	})
}

// Delete handles DELETE /categories/:id.
// Admin only. gradeID comes from the query param ?grade_id= because
// the URL already uses :id for the category.
// Returns 409 if the category still contains books.
func (h *CategoryHandler) Delete(c *gin.Context) {
	schoolID   := middleware.SchoolIDFromContext(c)
	categoryID := c.Param("id")
	gradeID    := c.Query("grade_id")

	if categoryID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "category id is required"})
		return
	}
	if gradeID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "grade_id query param is required"})
		return
	}

	err := h.categoryService.Delete(c.Request.Context(), schoolID, gradeID, categoryID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "category not found"})
		case errors.Is(err, service.ErrCategoryNotEmpty):
			c.JSON(http.StatusConflict, models.ErrorResponse{Error: err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to delete category"})
		}
		return
	}

	c.JSON(http.StatusOK, models.MessageResponse{Message: "category deleted successfully"})
}