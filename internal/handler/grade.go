package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/middleware"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/service"
)

// GradeHandler handles HTTP requests for grade management and login.
// Admin endpoints (Create, GetAll, Delete) require the admin role.
// The Login endpoint is public — it issues a grade JWT.
type GradeHandler struct {
	gradeService *service.GradeService
}

// NewGradeHandler creates a GradeHandler with its dependency injected.
func NewGradeHandler(gradeService *service.GradeService) *GradeHandler {
	return &GradeHandler{gradeService: gradeService}
}

// Login handles POST /auth/grade/login.
// Public endpoint — no JWT required. Returns a grade JWT on success.
// Grades identify with school_id + username.
func (h *GradeHandler) Login(c *gin.Context) {
	var req models.GradeLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	resp, err := h.gradeService.Login(c.Request.Context(), &req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrGradeInvalidCredentials):
			c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: err.Error()})
		case errors.Is(err, service.ErrGradeLocked):
			c.JSON(http.StatusTooManyRequests, models.ErrorResponse{Error: err.Error()})
		case errors.Is(err, service.ErrGradeInactive):
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "login failed"})
		}
		return
	}

	c.JSON(http.StatusOK, resp)
}

// Create handles POST /grades.
// Admin only — creates a grade account within the admin's school.
func (h *GradeHandler) Create(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)

	var req models.CreateGradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	grade, err := h.gradeService.Create(c.Request.Context(), schoolID, &req)
	if err != nil {
		if errors.Is(err, service.ErrGradeUsernameTaken) {
			c.JSON(http.StatusConflict, models.ErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to create grade"})
		return
	}

	c.JSON(http.StatusCreated, grade)
}

// GetAll handles GET /grades.
// Admin only — returns all grades in the admin's school.
func (h *GradeHandler) GetAll(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)

	grades, err := h.gradeService.GetAll(c.Request.Context(), schoolID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to fetch grades"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"grades": grades,
		"count":  len(grades),
	})
}

// Delete handles DELETE /grades/:id.
// Admin only — removes a grade and all its categories and books.
func (h *GradeHandler) Delete(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)
	gradeID  := c.Param("id")

	if gradeID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "grade id is required"})
		return
	}

	err := h.gradeService.Delete(c.Request.Context(), schoolID, gradeID)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "grade not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to delete grade"})
		return
	}

	c.JSON(http.StatusOK, models.MessageResponse{Message: "grade deleted successfully"})
}