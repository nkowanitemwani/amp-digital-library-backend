package models

import "time"

// =============================================================
// DOMAIN MODELS
// These structs mirror the database tables exactly.
// They are used by the repository layer to read from and write
// to the database. They are never returned directly from the API
// — response structs (defined below) control what the client sees.
// =============================================================

// School represents a registered school account.
// Each school is an independent tenant — all categories and books
// are scoped to a school's ID.
type School struct {
	ID           string     `db:"id"`
	Name         string     `db:"name"`
	Email        string     `db:"email"`
	PasswordHash string     `db:"password_hash"`
	Location     string     `db:"location"`
	IsActive     bool       `db:"is_active"`

	// Brute force protection fields — managed by the login service,
	// stored in the DB so all app instances share the same counters.
	LoginAttempts int        `db:"login_attempts"`
	LockedUntil  *time.Time `db:"locked_until"`  // nil means not locked
	LastLoginAt  *time.Time `db:"last_login_at"` // nil means never logged in

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Category represents a subject grouping defined by a school.
// e.g. "Mathematics", "Science", "English".
// Each school manages their own set of categories independently.
type Category struct {
	ID        string    `db:"id"`
	SchoolID  string    `db:"school_id"`
	Name      string    `db:"name"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Book represents a single learning unit within a category.
// A book is uploaded as a PDF, processed into audio in the background,
// and then served to students by unit number within its category.
type Book struct {
	ID         string     `db:"id"`
	SchoolID   string     `db:"school_id"`
	CategoryID string     `db:"category_id"`
	Title      string     `db:"title"`
	Author     string     `db:"author"`
	UnitNumber int        `db:"unit_number"`

	// Storage keys — relative paths used by the storage layer.
	// PDFPath is set immediately on upload.
	// AudioPath is set only after background processing completes.
	PDFPath   *string `db:"pdf_path"`   // nil until upload succeeds
	AudioPath *string `db:"audio_path"` // nil until processing completes

	// Status moves through: processing → ready | failed.
	// Defined as an ENUM in the DB — any value outside these three
	// is rejected at the database level.
	Status string `db:"status"`

	// Version is incremented on every update (optimistic locking).
	// All updates must include WHERE version = $current and verify
	// that exactly one row was affected, preventing silent overwrites
	// when two processes update the same book concurrently.
	Version int `db:"version"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// AuditEntry represents a single row written to the audit_log table.
// The audit log is append-only — entries are never updated or deleted.
// Metadata holds flexible key-value context (e.g. failure reason, IP address)
// without requiring schema changes.
type AuditEntry struct {
	SchoolID *string           `db:"school_id"` // nil for system-level events
	Action   string            `db:"action"`    // e.g. "book.created", "school.login.failed"
	Entity   string            `db:"entity"`    // e.g. "book", "category", "school"
	EntityID *string           `db:"entity_id"` // nil for events not tied to a specific row
	Metadata map[string]string // serialised to JSONB before insert
}

// =============================================================
// BOOK STATUS CONSTANTS
// Using constants instead of raw strings means a typo in
// application code is caught at compile time rather than
// silently writing a bad value to the database.
// =============================================================

const (
	BookStatusProcessing = "processing"
	BookStatusReady      = "ready"
	BookStatusFailed     = "failed"
)

// =============================================================
// REQUEST STRUCTS
// These define exactly what the API expects in the request body.
// Keeping them separate from domain models means a change to the
// DB schema does not automatically change the API contract.
// =============================================================

// RegisterSchoolRequest is the body expected by POST /auth/register.
type RegisterSchoolRequest struct {
	Name     string `json:"name"     binding:"required,min=2,max=100"`
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
	Location string `json:"location" binding:"max=200"`
}

// LoginRequest is the body expected by POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// CreateCategoryRequest is the body expected by POST /categories.
type CreateCategoryRequest struct {
	Name string `json:"name" binding:"required,min=1,max=50"`
}

// CreateBookRequest is the body expected by POST /books.
// The PDF file itself is received as a multipart form field, not JSON —
// these fields come from the other form fields in the same request.
type CreateBookRequest struct {
	CategoryID string `form:"category_id" binding:"required,uuid"`
	Title      string `form:"title"       binding:"required,min=1,max=200"`
	Author     string `form:"author"      binding:"max=100"`
	UnitNumber int    `form:"unit_number" binding:"required,min=1"`
}

// =============================================================
// RESPONSE STRUCTS
// These define exactly what the API returns to the client.
// Sensitive fields (password_hash, pdf_path, version, etc.) are
// deliberately excluded — the client never needs to see them.
// =============================================================

// SchoolResponse is returned after registration and in profile endpoints.
type SchoolResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Location  string    `json:"location"`
	CreatedAt time.Time `json:"created_at"`
}

// LoginResponse is returned after a successful login.
type LoginResponse struct {
	Token  string         `json:"token"`
	School SchoolResponse `json:"school"`
}

// CategoryResponse is returned for category list and detail endpoints.
type CategoryResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// BookResponse is returned for book list and detail endpoints.
// AudioURL is only populated when status is "ready" — the handler
// resolves the storage key to a full URL before building this struct.
type BookResponse struct {
	ID         string    `json:"id"`
	CategoryID string    `json:"category_id"`
	Title      string    `json:"title"`
	Author     string    `json:"author"`
	UnitNumber int       `json:"unit_number"`
	Status     string    `json:"status"`
	AudioURL   string    `json:"audio_url,omitempty"` // omitted until ready
	CreatedAt  time.Time `json:"created_at"`
}

// =============================================================
// SHARED RESPONSE HELPERS
// Simple, consistent response shapes used across all endpoints.
// =============================================================

// ErrorResponse is the standard error body returned on any failed request.
// Using a consistent shape means clients always know where to find
// the error message regardless of which endpoint failed.
type ErrorResponse struct {
	Error string `json:"error"`
}

// MessageResponse is used for simple success confirmations
// where there is no meaningful data to return (e.g. delete operations).
type MessageResponse struct {
	Message string `json:"message"`
}