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
// All endpoints are protected — schoolID is always read from the JWT
// context, never from the request body or URL.
type CategoryHandler struct {
	categoryService *service.CategoryService
}

// NewCategoryHandler creates a CategoryHandler with its dependency injected.
func NewCategoryHandler(categoryService *service.CategoryService) *CategoryHandler {
	return &CategoryHandler{categoryService: categoryService}
}

// Create handles POST /categories.
// Creates a new category for the authenticated school.
func (h *CategoryHandler) Create(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)

	var req models.CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	category, err := h.categoryService.Create(c.Request.Context(), schoolID, &req)
	if err != nil {
		if errors.Is(err, service.ErrCategoryNameTaken) {
			c.JSON(http.StatusConflict, models.ErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to create category"})
		return
	}

	c.JSON(http.StatusCreated, category)
}

// GetAll handles GET /categories.
// Returns all categories for the authenticated school ordered alphabetically.
// Always returns a JSON array — empty array when the school has no categories.
func (h *CategoryHandler) GetAll(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)

	categories, err := h.categoryService.GetAll(c.Request.Context(), schoolID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to fetch categories"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"categories": categories,
		"count":      len(categories),
	})
}

// Delete handles DELETE /categories/:id.
// Removes a category from the school's library.
// Returns 409 if the category still contains books — the admin must
// delete or move the books before deleting the category.
func (h *CategoryHandler) Delete(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)
	categoryID := c.Param("id")

	if categoryID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "category id is required"})
		return
	}

	err := h.categoryService.Delete(c.Request.Context(), schoolID, categoryID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "category not found"})
		case errors.Is(err, service.ErrCategoryNotEmpty):
			// 409 Conflict — the category has books and cannot be deleted yet.
			// The message tells the admin exactly what they need to do.
			c.JSON(http.StatusConflict, models.ErrorResponse{Error: err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to delete category"})
		}
		return
	}

	c.JSON(http.StatusOK, models.MessageResponse{Message: "category deleted successfully"})
}