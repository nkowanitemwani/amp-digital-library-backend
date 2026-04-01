package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/middleware"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/service"
)

// SchoolHandler handles HTTP requests for school registration and authentication.
// It is deliberately thin — it parses the request, calls the service,
// and writes the response. No business logic lives here.
type SchoolHandler struct {
	// schoolService is unexported — nothing outside this package can
	// bypass the handler and call the service directly via this struct.
	schoolService *service.SchoolService
}

// NewSchoolHandler creates a SchoolHandler with its dependency injected.
func NewSchoolHandler(schoolService *service.SchoolService) *SchoolHandler {
	return &SchoolHandler{schoolService: schoolService}
}

// Register handles POST /auth/register.
// Creates a new school account and returns the school profile.
// Does not issue a JWT — the school must log in after registering.
func (h *SchoolHandler) Register(c *gin.Context) {
	var req models.RegisterSchoolRequest

	// ShouldBindJSON validates the request body against the binding tags
	// defined on RegisterSchoolRequest (required, min/max lengths, email format).
	// Validation failures are returned as 400 before reaching the service.
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	school, err := h.schoolService.Register(c.Request.Context(), &req)
	if err != nil {
		// Map service-level sentinel errors to the correct HTTP status.
		// Any unrecognised error is a 500 — we do not leak its message.
		if errors.Is(err, service.ErrEmailTaken) {
			c.JSON(http.StatusConflict, models.ErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "registration failed"})
		return
	}

	c.JSON(http.StatusCreated, school)
}

// Login handles POST /auth/login.
// Verifies credentials and returns a JWT on success.
func (h *SchoolHandler) Login(c *gin.Context) {
	var req models.LoginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	resp, err := h.schoolService.Login(c.Request.Context(), &req)
	if err != nil {
		switch {
		// ErrInvalidCredentials covers both wrong email and wrong password —
		// the same 401 for both so callers cannot enumerate valid emails.
		case errors.Is(err, service.ErrInvalidCredentials):
			c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: err.Error()})
		case errors.Is(err, service.ErrAccountLocked):
			// 429 Too Many Requests — the account is temporarily locked.
			// The client should back off and retry after a delay.
			c.JSON(http.StatusTooManyRequests, models.ErrorResponse{Error: err.Error()})
		case errors.Is(err, service.ErrAccountInactive):
			c.JSON(http.StatusForbidden, models.ErrorResponse{Error: err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "login failed"})
		}
		return
	}

	c.JSON(http.StatusOK, resp)
}

// Me handles GET /auth/me.
// Returns the authenticated school's profile using the school_id
// extracted from the JWT by the auth middleware.
// This endpoint is useful for the frontend to confirm the token is
// still valid and retrieve up-to-date profile information on load.
func (h *SchoolHandler) Me(c *gin.Context) {
	// SchoolIDFromContext panics if called on an unprotected route —
	// that panic is intentional (see middleware/auth.go for reasoning).
	schoolID := middleware.SchoolIDFromContext(c)

	school, err := h.schoolService.GetByID(c.Request.Context(), schoolID)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			// The JWT was valid but the school no longer exists —
			// treat this as unauthorised rather than not found, since
			// a valid token for a deleted account should not be usable.
			c.JSON(http.StatusUnauthorized, models.ErrorResponse{Error: "account not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to fetch profile"})
		return
	}

	c.JSON(http.StatusOK, school)
}