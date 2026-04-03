package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/repository"
)

// =============================================================
// SCHOOL SERVICE
// Handles registration, login, and JWT generation.
// All business rules live here — the handler only parses HTTP,
// the repository only speaks SQL. This layer sits between them.
// =============================================================

// Login policy constants are defined here so they are visible and
// easy to adjust without hunting through business logic.
const (
	// maxLoginAttempts is the number of consecutive failures before
	// an account is temporarily locked.
	maxLoginAttempts = 5

	// lockoutDuration is how long an account stays locked after
	// hitting maxLoginAttempts.
	lockoutDuration = 15 * time.Minute

	// tokenExpiry is how long a JWT remains valid after issue.
	// Short enough to limit exposure if a token is stolen.
	tokenExpiry = 24 * time.Hour

	// bcryptCost controls the work factor for password hashing.
	// 12 is a good balance between security and hashing time (~300ms).
	// Increase this as hardware gets faster over time.
	bcryptCost = 12
)

// Sentinel errors returned to the handler so it can map them to the
// correct HTTP status code without inspecting error strings.
var (
	// ErrEmailTaken is returned by Register when the email already exists.
	ErrEmailTaken = errors.New("email address is already registered")

	// ErrInvalidCredentials is returned by Login when email or password
	// is wrong. We deliberately use the same error for both cases —
	// telling an attacker which one was wrong is a security leak.
	ErrInvalidCredentials = errors.New("invalid email or password")

	// ErrAccountLocked is returned when the account is temporarily locked
	// after too many failed login attempts.
	ErrAccountLocked = errors.New("account is temporarily locked due to too many failed login attempts")

	// ErrAccountInactive is returned when a school account has been
	// suspended by an administrator.
	ErrAccountInactive = errors.New("account is inactive")
)

// SchoolService handles school registration and authentication.
type SchoolService struct {
	schoolRepo *repository.SchoolRepository
	auditRepo  *repository.AuditRepository
	jwtSecret  []byte
}

// NewSchoolService creates a SchoolService with its dependencies.
// jwtSecret is passed in from config — the service never reads
// environment variables directly.
func NewSchoolService(
	schoolRepo *repository.SchoolRepository,
	auditRepo *repository.AuditRepository,
	jwtSecret string,
) *SchoolService {
	return &SchoolService{
		schoolRepo: schoolRepo,
		auditRepo:  auditRepo,
		jwtSecret:  []byte(jwtSecret),
	}
}

// Register creates a new school account.
// Returns the created school as a SchoolResponse (no password hash).
// Returns ErrEmailTaken if the email is already in use.
func (s *SchoolService) Register(ctx context.Context, req *models.RegisterSchoolRequest) (*models.SchoolResponse, error) {
	// Check for an existing account before hashing — hashing is
	// deliberately slow, so we avoid the cost on a duplicate email.
	existing, err := s.schoolRepo.GetByEmail(ctx, req.Email)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("check existing email: %w", err)
	}
	if existing != nil {
		return nil, ErrEmailTaken
	}

	// Hash the password with bcrypt. The cost factor controls how slow
	// hashing is — slower means harder for an attacker to brute force
	// leaked hashes offline.
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	school, err := s.schoolRepo.Create(ctx, &models.School{
		Name:         req.Name,
		Email:        req.Email,
		PasswordHash: string(hash),
		Location:     req.Location,
	})
	if err != nil {
		return nil, fmt.Errorf("create school: %w", err)
	}

	s.auditRepo.Log(ctx, &models.AuditEntry{
		SchoolID: &school.ID,
		Action:   "school.registered",
		Entity:   "school",
		EntityID: &school.ID,
	})

	return toSchoolResponse(school), nil
}

// Login verifies credentials and returns a JWT on success.
// It enforces account lockout and records every attempt in the DB
// so the counters are shared across all running app instances.
func (s *SchoolService) Login(ctx context.Context, req *models.LoginRequest) (*models.LoginResponse, error) {
	school, err := s.schoolRepo.GetByEmail(ctx, req.Email)
	if errors.Is(err, repository.ErrNotFound) {
		// No account with this email — return the same error as a wrong
		// password so an attacker cannot enumerate valid emails.
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("get school by email: %w", err)
	}

	// Check account status before anything else — an inactive account
	// should not even reach the password check.
	if !school.IsActive {
		return nil, ErrAccountInactive
	}

	// Check whether the account is currently locked.
	// LockedUntil is a pointer — nil means never locked.
	if school.LockedUntil != nil && time.Now().Before(*school.LockedUntil) {
		s.auditRepo.Log(ctx, &models.AuditEntry{
			SchoolID: &school.ID,
			Action:   "school.login.blocked",
			Entity:   "school",
			EntityID: &school.ID,
			Metadata: map[string]string{
				"locked_until": school.LockedUntil.Format(time.RFC3339),
			},
		})
		return nil, ErrAccountLocked
	}

	// Verify the password. bcrypt.CompareHashAndPassword is constant-time —
	// it takes the same amount of time regardless of where the mismatch is,
	// preventing timing attacks that could reveal partial password information.
	if err := bcrypt.CompareHashAndPassword([]byte(school.PasswordHash), []byte(req.Password)); err != nil {
		// Wrong password — increment the failure counter.
		// The repo handles the lockout logic atomically in a single UPDATE.
		if dbErr := s.schoolRepo.RecordFailedLogin(ctx, school.ID, maxLoginAttempts, lockoutDuration); dbErr != nil {
			// Log but do not surface — the caller still gets ErrInvalidCredentials.
			fmt.Printf("record failed login: %v\n", dbErr)
		}

		s.auditRepo.Log(ctx, &models.AuditEntry{
			SchoolID: &school.ID,
			Action:   "school.login.failed",
			Entity:   "school",
			EntityID: &school.ID,
		})

		return nil, ErrInvalidCredentials
	}

	// Password correct — reset the failure counters and record the login time.
	if err := s.schoolRepo.RecordSuccessfulLogin(ctx, school.ID); err != nil {
		// Non-fatal — the login can still succeed even if this update fails.
		fmt.Printf("record successful login: %v\n", err)
	}

	token, err := s.generateToken(school.ID, school.ID, models.RoleAdmin)
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	s.auditRepo.Log(ctx, &models.AuditEntry{
		SchoolID: &school.ID,
		Action:   "school.login.success",
		Entity:   "school",
		EntityID: &school.ID,
	})

	return &models.LoginResponse{
		Token:  token,
		School: *toSchoolResponse(school),
	}, nil
}

// GetByID returns a school's public profile by ID.
// Used by the middleware to confirm the school from a JWT still exists.
func (s *SchoolService) GetByID(ctx context.Context, id string) (*models.SchoolResponse, error) {
	school, err := s.schoolRepo.GetByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get school by id: %w", err)
	}

	return toSchoolResponse(school), nil
}

// =============================================================
// JWT HELPERS
// Token generation and validation are private to this service —
// the middleware calls ValidateToken, nothing else needs to.
// =============================================================

// jwtClaims are the fields embedded in the JWT payload.
// role distinguishes admin (school) from grade (shared class account)
// so middleware can enforce route-level access control without a DB lookup.
// school_id is always present — for grades it is the school they belong to,
// allowing them to read that school's content.
type jwtClaims struct {
	SchoolID  string `json:"school_id"`
	SubjectID string `json:"subject_id"` // school.id for admin, grade.id for grade
	Role      string `json:"role"`        // models.RoleAdmin or models.RoleGrade
	jwt.RegisteredClaims
}

// generateToken creates a signed JWT for either an admin or a grade account.
// subjectID is the ID of the entity logging in (school ID for admins,
// grade ID for grades). role controls which routes they can access.
func (s *SchoolService) generateToken(schoolID, subjectID, role string) (string, error) {
	claims := jwtClaims{
		SchoolID:  schoolID,
		SubjectID: subjectID,
		Role:      role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}

	return signed, nil
}

// ValidateToken parses and validates a JWT string, returning the
// school_id, subject_id and role embedded in it. Called by the auth
// middleware on every protected request.
// Returns an error if the token is expired, malformed, or has an
// invalid signature.
func (s *SchoolService) ValidateToken(tokenStr string) (schoolID, subjectID, role string, err error) {
	token, parseErr := jwt.ParseWithClaims(tokenStr, &jwtClaims{}, func(t *jwt.Token) (any, error) {
		// Explicitly verify the signing method — rejecting tokens signed
		// with a different algorithm prevents algorithm-confusion attacks
		// where an attacker swaps HS256 for "none" or RS256.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if parseErr != nil {
		return "", "", "", fmt.Errorf("invalid token: %w", parseErr)
	}

	claims, ok := token.Claims.(*jwtClaims)
	if !ok || !token.Valid {
		return "", "", "", fmt.Errorf("invalid token claims")
	}

	return claims.SchoolID, claims.SubjectID, claims.Role, nil
}

// =============================================================
// RESPONSE BUILDER
// Converts the internal School model to the public SchoolResponse,
// deliberately excluding sensitive fields like password_hash.
// Keeping this in the service (not the handler) means every code
// path that returns school data uses the same safe conversion.
// =============================================================

func toSchoolResponse(s *models.School) *models.SchoolResponse {
	return &models.SchoolResponse{
		ID:        s.ID,
		Name:      s.Name,
		Email:     s.Email,
		Location:  s.Location,
		CreatedAt: s.CreatedAt,
	}
}

// ErrNotFound is re-exported from this package so handlers only need
// to import service, not both service and repository.
var ErrNotFound = repository.ErrNotFound