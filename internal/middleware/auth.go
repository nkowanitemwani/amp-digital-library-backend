package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/service"
)

type contextKey string

const (
	// schoolIDKey — always the school the authenticated entity belongs to.
	schoolIDKey contextKey = "school_id"

	// subjectIDKey — the ID of the entity that logged in.
	// Admin: school UUID. Grade: grade UUID.
	subjectIDKey contextKey = "subject_id"

	// roleKey — models.RoleAdmin or models.RoleGrade.
	roleKey contextKey = "role"
)

// RequireAuth validates the JWT and stores school_id, subject_id, and
// role in the request context. Must precede RequireAdmin or RequireGrade.
func RequireAuth(schoolService *service.SchoolService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: "missing Authorization header",
			})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: "Authorization header must be in the format: Bearer <token>",
			})
			c.Abort()
			return
		}

		schoolID, subjectID, role, err := schoolService.ValidateToken(parts[1])
		if err != nil {
			c.JSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: "invalid or expired token",
			})
			c.Abort()
			return
		}

		c.Set(string(schoolIDKey),  schoolID)
		c.Set(string(subjectIDKey), subjectID)
		c.Set(string(roleKey),      role)

		c.Next()
	}
}

// RequireAdmin aborts with 403 if the authenticated entity is not an admin.
// Must follow RequireAuth in the middleware chain.
func RequireAdmin(c *gin.Context) {
	role, _ := c.Get(string(roleKey))
	if role != models.RoleAdmin {
		c.JSON(http.StatusForbidden, models.ErrorResponse{
			Error: "this action requires an admin account",
		})
		c.Abort()
		return
	}
	c.Next()
}

// RequireGrade aborts with 403 if the authenticated entity is not a grade.
// Must follow RequireAuth in the middleware chain.
func RequireGrade(c *gin.Context) {
	role, _ := c.Get(string(roleKey))
	if role != models.RoleGrade {
		c.JSON(http.StatusForbidden, models.ErrorResponse{
			Error: "this route requires a grade account",
		})
		c.Abort()
		return
	}
	c.Next()
}

// SchoolIDFromContext retrieves the school_id from the Gin context.
// Always present after RequireAuth on any protected route.
func SchoolIDFromContext(c *gin.Context) string {
	val, exists := c.Get(string(schoolIDKey))
	if !exists {
		panic("SchoolIDFromContext called on unprotected route — ensure RequireAuth middleware is applied")
	}
	schoolID, ok := val.(string)
	if !ok || schoolID == "" {
		panic("school_id in context is not a valid string — this is a bug in the auth middleware")
	}
	return schoolID
}

// GradeIDFromContext retrieves the grade_id (subject_id) from the context.
// Only valid on routes protected by RequireGrade — panics otherwise
// so wiring mistakes are caught immediately in development.
func GradeIDFromContext(c *gin.Context) string {
	role, _ := c.Get(string(roleKey))
	if role != models.RoleGrade {
		panic("GradeIDFromContext called on a non-grade route — use RequireGrade middleware")
	}
	val, exists := c.Get(string(subjectIDKey))
	if !exists {
		panic("subject_id not found in context — this is a bug in the auth middleware")
	}
	gradeID, ok := val.(string)
	if !ok || gradeID == "" {
		panic("subject_id in context is not a valid string — this is a bug in the auth middleware")
	}
	return gradeID
}

// RoleFromContext retrieves the role from the Gin context.
func RoleFromContext(c *gin.Context) string {
	val, _ := c.Get(string(roleKey))
	role, _ := val.(string)
	return role
}