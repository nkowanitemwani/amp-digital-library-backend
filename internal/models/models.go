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
// The admin logs in with email + password and manages grades,
// categories, and books for their school.
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

// Grade represents a shared login account for an entire class.
// e.g. "Grade 3" has one username/password used by all students
// in that class simultaneously in the computer lab.
// Categories and books are scoped to a grade so students only
// see content appropriate for their level.
type Grade struct {
	ID           string     `db:"id"`
	SchoolID     string     `db:"school_id"`
	Name         string     `db:"name"`     // display name, e.g. "Grade 3"
	Username     string     `db:"username"` // login handle, e.g. "grade3"
	PasswordHash string     `db:"password_hash"`
	IsActive     bool       `db:"is_active"`

	// Brute force protection — shared counters mean all simultaneous
	// logins from the same grade share the same lockout state.
	LoginAttempts int        `db:"login_attempts"`
	LockedUntil  *time.Time `db:"locked_until"`
	LastLoginAt  *time.Time `db:"last_login_at"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Category represents a subject grouping within a grade.
// e.g. Grade 3 has "Mathematics", "Science", "English".
// Scoped to a grade — Grade 3 and Grade 4 have independent category lists.
type Category struct {
	ID       string `db:"id"`
	SchoolID string `db:"school_id"`
	GradeID  string `db:"grade_id"`
	Name     string `db:"name"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Book represents a single learning unit within a category.
// A book is uploaded as a PDF, processed into audio in the background,
// and then served to students by unit number within its category.
type Book struct {
	ID         string `db:"id"`
	SchoolID   string `db:"school_id"`
	GradeID    string `db:"grade_id"`
	CategoryID string `db:"category_id"`
	Title      string `db:"title"`
	Author     string `db:"author"`
	UnitNumber int    `db:"unit_number"`

	// Storage keys — relative paths used by the storage layer.
	// PDFPath is set immediately on upload.
	// AudioPath is set only after standard audio processing completes.
	// DialogueAudioPath is set after two-voice dialogue synthesis completes.
	PDFPath           *string `db:"pdf_path"`
	AudioPath         *string `db:"audio_path"`
	DialogueAudioPath *string `db:"dialogue_audio_path"`

	// Status tracks standard audio generation.
	// DialogueStatus tracks the two-voice dialogue independently —
	// both pipelines run in parallel so one can be ready before the other.
	Status          string `db:"status"`
	DialogueStatus  string `db:"dialogue_status"`

	// Optimistic locking — incremented on every update.
	Version int `db:"version"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Question represents a single AI-generated multiple choice question
// for a book's quiz. The question and all four options are synthesised
// to audio 
type Question struct {
	ID                string      `db:"id"`
	BookID            string      `db:"book_id"`
	GradeID           string      `db:"grade_id"`
	QuestionText      string      `db:"question_text"`
	Options           []string    // unmarshalled from JSONB
	CorrectIndex      int         `db:"correct_index"`
	QuestionAudioPath *string     `db:"question_audio_path"`
	AudioStatus       string      `db:"audio_status"`
	OrderIndex        int         `db:"order_index"`
	CreatedAt         time.Time   `db:"created_at"`
}

// QuizAttempt records one completion of a book's quiz by a grade.
// Multiple attempts are stored so teachers can track improvement.
type QuizAttempt struct {
	ID          string    `db:"id"`
	BookID      string    `db:"book_id"`
	GradeID     string    `db:"grade_id"`
	Score       int       `db:"score"`
	Total       int       `db:"total"`
	Answers     []int     // unmarshalled from JSONB — selected option index per question
	CompletedAt time.Time `db:"completed_at"`
}

// AuditEntry represents a single row written to the audit_log table.
// The audit log is append-only — entries are never updated or deleted.
// Metadata holds flexible key-value context (e.g. failure reason, IP address)
// without requiring schema changes.
type AuditEntry struct {
	SchoolID *string           `db:"school_id"` // nil for system-level events
	Action   string            `db:"action"`    // e.g. "book.created", "grade.login.failed"
	Entity   string            `db:"entity"`    // e.g. "book", "category", "grade"
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
	BookStatusPending    = "pending"    // inserted, PDF upload not yet complete
	BookStatusProcessing = "processing" // PDF saved, queued for audio conversion
	BookStatusReady      = "ready"
	BookStatusFailed     = "failed"
)

// =============================================================
// ROLE CONSTANTS
// Embedded in the JWT so middleware can distinguish admin
// (school account) from grade (shared student account) without
// a DB lookup on every request.
// =============================================================

const (
	RoleAdmin = "admin"
	RoleGrade = "grade"
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

// CreateGradeRequest is the body expected by POST /grades.
// Only a logged-in admin can create grade accounts.
type CreateGradeRequest struct {
	Name     string `json:"name"     binding:"required,min=1,max=50"`
	Username string `json:"username" binding:"required,min=2,max=50"`
	Password string `json:"password" binding:"required,min=6"`
}

// GradeLoginRequest is the body expected by POST /auth/grade/login.
// Grades identify by school_id + username. school_id is required
// because usernames are only unique within a school — two schools
// can both have a grade with username "grade3".
type GradeLoginRequest struct {
	SchoolID string `json:"school_id" binding:"required,uuid"`
	Username string `json:"username"  binding:"required"`
	Password string `json:"password"  binding:"required"`
}

// CreateCategoryRequest is the body expected by POST /categories.
// GradeID tells the API which grade this category belongs to.
type CreateCategoryRequest struct {
	GradeID string `json:"grade_id" binding:"required,uuid"`
	Name    string `json:"name"     binding:"required,min=1,max=50"`
}

// CreateBookRequest is the body expected by POST /books.
// The PDF file itself is received as a multipart form field, not JSON —
// these fields come from the other form fields in the same request.
type CreateBookRequest struct {
	GradeID    string `form:"grade_id"    binding:"required,uuid"`
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

// LoginResponse is returned after a successful admin login.
type LoginResponse struct {
	Token  string         `json:"token"`
	School SchoolResponse `json:"school"`
}

// GradeResponse is returned for grade list and detail endpoints.
// password_hash is never included.
type GradeResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Username  string    `json:"username"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// GradeLoginResponse is returned after a successful grade login.
type GradeLoginResponse struct {
	Token    string        `json:"token"`
	Grade    GradeResponse `json:"grade"`
	SchoolID string        `json:"school_id"`
}

// CategoryResponse is returned for category list and detail endpoints.
type CategoryResponse struct {
	ID        string    `json:"id"`
	GradeID   string    `json:"grade_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// BookResponse is returned for book list and detail endpoints.
// AudioURL is only populated when status is "ready" — the handler
// resolves the storage key to a full URL before building this struct.
type BookResponse struct {
	ID         string    `json:"id"`
	GradeID    string    `json:"grade_id"`
	CategoryID string    `json:"category_id"`
	Title      string    `json:"title"`
	Author     string    `json:"author"`
	UnitNumber int       `json:"unit_number"`
	Status     string    `json:"status"`
	AudioURL   string    `json:"audio_url,omitempty"` // omitted until ready
	CreatedAt  time.Time `json:"created_at"`
}

// SubmitAttemptRequest is the body expected by POST /student/books/:id/attempts.
// Answers is an array of selected option indices, one per question.
// e.g. [0, 2, 1, 3] — selected option 0 for Q1, option 2 for Q2, etc.
type SubmitAttemptRequest struct {
	Answers []int `json:"answers" binding:"required"`
}

// QuestionResponse is returned by GET /student/books/:id/questions.
// CorrectIndex is excluded — students must not receive the answer
// before submitting. It is only included in AttemptResponse after submission.
type QuestionResponse struct {
	ID               string   `json:"id"`
	QuestionText     string   `json:"question_text"`
	Options          []string `json:"options"`
	OrderIndex       int      `json:"order_index"`
	QuestionAudioURL string   `json:"question_audio_url,omitempty"`
}

// AttemptResponse is returned after a quiz is submitted.
// Includes correct answers so the student (via their teacher) can
// review which questions they got right.
type AttemptResponse struct {
	Score     int   `json:"score"`
	Total     int   `json:"total"`
	Answers   []int `json:"answers"`
	Correct   []int `json:"correct"`   // correct_index per question, in order
	Passed    bool  `json:"passed"`    // true if score >= 75%
}

// GradeProgressResponse is returned by GET /admin/grades/:id/progress.
// Gives the teacher a summary of quiz activity across all books.
type GradeProgressResponse struct {
	GradeID   string         `json:"grade_id"`
	GradeName string         `json:"grade_name"`
	Books     []BookProgress `json:"books"`
}

// BookProgress is one book's quiz summary within a GradeProgressResponse.
type BookProgress struct {
	BookID      string  `json:"book_id"`
	Title       string  `json:"title"`
	UnitNumber  int     `json:"unit_number"`
	Attempts    int     `json:"attempts"`
	BestScore   int     `json:"best_score"`
	TotalQ      int     `json:"total_questions"`
	AvgScore    float64 `json:"avg_score"`
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
