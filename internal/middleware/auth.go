package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/service"
)

// contextKey is an unexported type used as the key for values stored
// in the request context. Using a custom type (not a plain string)
// prevents key collisions if another package stores values with the
// same string key — the types must match, not just the values.
type contextKey string

const (
	// schoolIDKey is the context key under which the authenticated
	// school's ID is stored after a successful JWT validation.
	// Handlers retrieve it with SchoolIDFromContext().
	schoolIDKey contextKey = "school_id"
)

// RequireAuth is a Gin middleware that validates the JWT on every
// protected route. It must be attached to any route group that requires
// a logged-in school.
//
// On success: extracts the school_id from the token and stores it in
// the request context, then calls c.Next() to continue to the handler.
//
// On failure: writes a 401 response and calls c.Abort() so no further
// handlers in the chain are executed — the request stops here.
//
// schoolService is passed in (not global) so this middleware can be
// tested independently by injecting a mock service.
func RequireAuth(schoolService *service.SchoolService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract the token from the Authorization header.
		// The expected format is: "Bearer <token>"
		// Any other format is rejected immediately.
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: "missing Authorization header",
			})
			c.Abort()
			return
		}

		// Split on a single space — "Bearer" and the token.
		// We check for exactly two parts to reject malformed headers
		// like "Bearer" with no token, or multiple spaces.
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: "Authorization header must be in the format: Bearer <token>",
			})
			c.Abort()
			return
		}

		tokenStr := parts[1]

		// ValidateToken verifies the signature, checks the expiry, and
		// returns the school_id embedded in the claims.
		schoolID, err := schoolService.ValidateToken(tokenStr)
		if err != nil {
			// Do not include the raw error — it may contain internal details
			// about the JWT library. A generic message is enough for the client.
			c.JSON(http.StatusUnauthorized, models.ErrorResponse{
				Error: "invalid or expired token",
			})
			c.Abort()
			return
		}

		// Store the school_id in the context so handlers can retrieve it
		// without re-parsing the token. The handler must never accept
		// school_id from the request body — always read it from here.
		c.Set(string(schoolIDKey), schoolID)

		c.Next()
	}
}

// SchoolIDFromContext retrieves the authenticated school's ID from the
// Gin context. Call this in every protected handler instead of reading
// school_id from the request body or URL parameters.
//
// Panics if called on a route that does not have RequireAuth middleware —
// this is intentional. A missing school_id on a protected route is a
// programming error, not a runtime error, and should be caught immediately
// during development rather than silently returning empty strings in production.
func SchoolIDFromContext(c *gin.Context) string {
	val, exists := c.Get(string(schoolIDKey))
	if !exists {
		// This should never happen on a route protected by RequireAuth.
		// If it does, it means a handler was accidentally registered
		// outside the auth middleware group.
		panic("SchoolIDFromContext called on unprotected route — ensure RequireAuth middleware is applied")
	}

	schoolID, ok := val.(string)
	if !ok || schoolID == "" {
		panic("school_id in context is not a valid string — this is a bug in the auth middleware")
	}

	return schoolID
}