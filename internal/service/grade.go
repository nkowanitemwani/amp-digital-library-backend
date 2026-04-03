package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/repository"
)

// =============================================================
// GRADE SERVICE
// Handles grade account creation, listing, deletion, and login.
// Admins create and manage grade accounts.
// Students share a grade account to log in simultaneously.
// =============================================================

var (
	// ErrGradeUsernameTaken is returned when the username already
	// exists within this school.
	ErrGradeUsernameTaken = errors.New("a grade with this username already exists in your school")

	// ErrGradeInvalidCredentials covers wrong username and wrong
	// password — same message for both to prevent username enumeration.
	ErrGradeInvalidCredentials = errors.New("invalid username or password")

	// ErrGradeLocked is returned when the grade account is temporarily
	// locked after too many failed login attempts.
	ErrGradeLocked = errors.New("this account is temporarily locked due to too many failed login attempts — please try again later")

	// ErrGradeInactive is returned when the grade has been deactivated.
	ErrGradeInactive = errors.New("this grade account is inactive — please speak to your teacher")
)

// GradeService handles grade CRUD and authentication.
type GradeService struct {
	gradeRepo     *repository.GradeRepository
	auditRepo     *repository.AuditRepository
	schoolService *SchoolService
}

// NewGradeService creates a GradeService with its dependencies.
func NewGradeService(
	gradeRepo *repository.GradeRepository,
	auditRepo *repository.AuditRepository,
	schoolService *SchoolService,
) *GradeService {
	return &GradeService{
		gradeRepo:     gradeRepo,
		auditRepo:     auditRepo,
		schoolService: schoolService,
	}
}

// Create adds a new grade account to a school.
// Only admins can call this — route middleware enforces that.
// schoolID comes from the admin's JWT, never from the request body.
func (s *GradeService) Create(ctx context.Context, schoolID string, req *models.CreateGradeRequest) (*models.GradeResponse, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash grade password: %w", err)
	}

	grade, err := s.gradeRepo.Create(ctx, &models.Grade{
		SchoolID:     schoolID,
		Name:         req.Name,
		Username:     req.Username,
		PasswordHash: string(hash),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrGradeUsernameTaken
		}
		return nil, fmt.Errorf("create grade: %w", err)
	}

	s.auditRepo.Log(ctx, &models.AuditEntry{
		SchoolID: &schoolID,
		Action:   "grade.created",
		Entity:   "grade",
		EntityID: &grade.ID,
		Metadata: map[string]string{
			"name":     grade.Name,
			"username": grade.Username,
		},
	})

	return toGradeResponse(grade), nil
}

// GetAll returns all grades in a school ordered by name.
func (s *GradeService) GetAll(ctx context.Context, schoolID string) ([]*models.GradeResponse, error) {
	grades, err := s.gradeRepo.GetAllBySchool(ctx, schoolID)
	if err != nil {
		return nil, fmt.Errorf("get grades: %w", err)
	}

	responses := make([]*models.GradeResponse, len(grades))
	for i, g := range grades {
		responses[i] = toGradeResponse(g)
	}

	return responses, nil
}

// Delete removes a grade account and all its categories and books
// (via ON DELETE CASCADE in the DB).
// schoolID from the JWT ensures an admin cannot delete another school's grades.
func (s *GradeService) Delete(ctx context.Context, schoolID, gradeID string) error {
	err := s.gradeRepo.Delete(ctx, gradeID, schoolID)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete grade: %w", err)
	}

	s.auditRepo.Log(ctx, &models.AuditEntry{
		SchoolID: &schoolID,
		Action:   "grade.deleted",
		Entity:   "grade",
		EntityID: &gradeID,
	})

	return nil
}

// Login verifies a grade's credentials and returns a JWT on success.
// The grade JWT carries role = "grade", school_id, and grade_id so
// students can read their grade's categories and books without extra
// DB lookups on every request.
func (s *GradeService) Login(ctx context.Context, req *models.GradeLoginRequest) (*models.GradeLoginResponse, error) {
	grade, err := s.gradeRepo.GetByUsername(ctx, req.SchoolID, req.Username)
	if errors.Is(err, repository.ErrNotFound) {
		// Same error for wrong school_id, wrong username, and wrong
		// password — prevents attackers from enumerating valid usernames.
		return nil, ErrGradeInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("get grade by username: %w", err)
	}

	if !grade.IsActive {
		return nil, ErrGradeInactive
	}

	// Check lockout before the expensive bcrypt comparison.
	if grade.LockedUntil != nil && time.Now().Before(*grade.LockedUntil) {
		s.auditRepo.Log(ctx, &models.AuditEntry{
			SchoolID: &grade.SchoolID,
			Action:   "grade.login.blocked",
			Entity:   "grade",
			EntityID: &grade.ID,
		})
		return nil, ErrGradeLocked
	}

	if err := bcrypt.CompareHashAndPassword([]byte(grade.PasswordHash), []byte(req.Password)); err != nil {
		if dbErr := s.gradeRepo.RecordFailedLogin(ctx, grade.ID, maxLoginAttempts, lockoutDuration); dbErr != nil {
			fmt.Printf("record grade failed login: %v\n", dbErr)
		}
		s.auditRepo.Log(ctx, &models.AuditEntry{
			SchoolID: &grade.SchoolID,
			Action:   "grade.login.failed",
			Entity:   "grade",
			EntityID: &grade.ID,
		})
		return nil, ErrGradeInvalidCredentials
	}

	if err := s.gradeRepo.RecordSuccessfulLogin(ctx, grade.ID); err != nil {
		fmt.Printf("record grade successful login: %v\n", err)
	}

	// Generate JWT with role = "grade" and grade_id as the subject.
	// school_id is embedded so students can read their school's content.
	// grade_id is passed as subjectID so middleware can put it in context.
	token, err := s.schoolService.generateToken(grade.SchoolID, grade.ID, models.RoleGrade)
	if err != nil {
		return nil, fmt.Errorf("generate grade token: %w", err)
	}

	s.auditRepo.Log(ctx, &models.AuditEntry{
		SchoolID: &grade.SchoolID,
		Action:   "grade.login.success",
		Entity:   "grade",
		EntityID: &grade.ID,
	})

	return &models.GradeLoginResponse{
		Token:    token,
		Grade:    *toGradeResponse(grade),
		SchoolID: grade.SchoolID,
	}, nil
}

// =============================================================
// RESPONSE BUILDER
// =============================================================

func toGradeResponse(g *models.Grade) *models.GradeResponse {
	return &models.GradeResponse{
		ID:        g.ID,
		Name:      g.Name,
		Username:  g.Username,
		IsActive:  g.IsActive,
		CreatedAt: g.CreatedAt,
	}
}