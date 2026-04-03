package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/repository"
)

// =============================================================
// CATEGORY SERVICE
// Business rules for managing categories within a grade.
// Categories are scoped to a grade — a grade can only see and
// manage its own categories.
// =============================================================

var (
	// ErrCategoryNameTaken is returned when a grade already has a
	// category with this name (CITEXT makes the check case-insensitive).
	ErrCategoryNameTaken = errors.New("a category with this name already exists in this grade")

	// ErrCategoryNotEmpty is returned when trying to delete a category
	// that still contains books. The DB RESTRICT FK produces this.
	ErrCategoryNotEmpty = errors.New("category cannot be deleted while it still contains books — delete the books first")
)

// CategoryService handles CRUD operations for categories within grades.
type CategoryService struct {
	categoryRepo *repository.CategoryRepository
	gradeRepo    *repository.GradeRepository
	auditRepo    *repository.AuditRepository
}

// NewCategoryService creates a CategoryService with its dependencies.
func NewCategoryService(
	categoryRepo *repository.CategoryRepository,
	gradeRepo *repository.GradeRepository,
	auditRepo *repository.AuditRepository,
) *CategoryService {
	return &CategoryService{
		categoryRepo: categoryRepo,
		gradeRepo:    gradeRepo,
		auditRepo:    auditRepo,
	}
}

// Create adds a new category to a grade.
// schoolID and gradeID both come from the JWT — never from the request body.
// gradeID is verified to belong to the school before creating.
func (s *CategoryService) Create(ctx context.Context, schoolID, gradeID string, req *models.CreateCategoryRequest) (*models.CategoryResponse, error) {
	// Verify the grade belongs to this school before creating a category in it.
	// Prevents an admin from creating categories in another school's grade
	// even if they somehow know the grade UUID.
	if _, err := s.gradeRepo.GetByID(ctx, gradeID, schoolID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("verify grade: %w", err)
	}

	cat, err := s.categoryRepo.Create(ctx, schoolID, gradeID, req.Name)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrCategoryNameTaken
		}
		return nil, fmt.Errorf("create category: %w", err)
	}

	s.auditRepo.Log(ctx, &models.AuditEntry{
		SchoolID: &schoolID,
		Action:   "category.created",
		Entity:   "category",
		EntityID: &cat.ID,
		Metadata: map[string]string{
			"name":     cat.Name,
			"grade_id": gradeID,
		},
	})

	return toCategoryResponse(cat), nil
}

// GetAll returns all categories for a grade, ordered alphabetically.
// gradeID is verified to belong to the school before querying.
func (s *CategoryService) GetAll(ctx context.Context, schoolID, gradeID string) ([]*models.CategoryResponse, error) {
	// Ownership check — grade must belong to this school.
	if _, err := s.gradeRepo.GetByID(ctx, gradeID, schoolID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("verify grade: %w", err)
	}

	categories, err := s.categoryRepo.GetAllByGrade(ctx, gradeID)
	if err != nil {
		return nil, fmt.Errorf("get categories: %w", err)
	}

	responses := make([]*models.CategoryResponse, len(categories))
	for i, cat := range categories {
		responses[i] = toCategoryResponse(cat)
	}

	return responses, nil
}

// Delete removes a category from a grade.
// Returns ErrCategoryNotEmpty if books still exist in it.
func (s *CategoryService) Delete(ctx context.Context, schoolID, gradeID, categoryID string) error {
	// Verify grade ownership before attempting deletion.
	if _, err := s.gradeRepo.GetByID(ctx, gradeID, schoolID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("verify grade: %w", err)
	}

	err := s.categoryRepo.Delete(ctx, categoryID, gradeID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		if isForeignKeyViolation(err) {
			return ErrCategoryNotEmpty
		}
		return fmt.Errorf("delete category: %w", err)
	}

	s.auditRepo.Log(ctx, &models.AuditEntry{
		SchoolID: &schoolID,
		Action:   "category.deleted",
		Entity:   "category",
		EntityID: &categoryID,
	})

	return nil
}

// =============================================================
// RESPONSE BUILDER
// =============================================================

func toCategoryResponse(c *models.Category) *models.CategoryResponse {
	return &models.CategoryResponse{
		ID:        c.ID,
		GradeID:   c.GradeID,
		Name:      c.Name,
		CreatedAt: c.CreatedAt,
	}
}