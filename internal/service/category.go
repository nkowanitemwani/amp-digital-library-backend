package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/repository"
)

// =============================================================
// CATEGORY SERVICE
// Business rules for managing a school's categories.
// The repository enforces uniqueness at the DB level — this service
// catches that constraint and returns a clean error to the handler.
// =============================================================

// Sentinel errors specific to categories.
var (
	// ErrCategoryNameTaken is returned when a school tries to create
	// a category with a name they already have. CITEXT in the DB means
	// "Science" and "science" are treated as the same name.
	ErrCategoryNameTaken = errors.New("a category with this name already exists")

	// ErrCategoryNotEmpty is returned when a school tries to delete a
	// category that still has books in it. The DB RESTRICT foreign key
	// on books.category_id produces this condition — we translate the
	// raw DB error into a meaningful message for the client.
	ErrCategoryNotEmpty = errors.New("category cannot be deleted while it still contains books")
)

// CategoryService handles CRUD operations for school categories.
type CategoryService struct {
	categoryRepo *repository.CategoryRepository
	auditRepo    *repository.AuditRepository
}

// NewCategoryService creates a CategoryService with its dependencies.
func NewCategoryService(
	categoryRepo *repository.CategoryRepository,
	auditRepo *repository.AuditRepository,
) *CategoryService {
	return &CategoryService{
		categoryRepo: categoryRepo,
		auditRepo:    auditRepo,
	}
}

// Create adds a new category to a school's library.
// schoolID comes from the JWT in the middleware — the handler never
// accepts it from the request body, so a school cannot create categories
// for another school even if they construct a malicious request.
func (s *CategoryService) Create(ctx context.Context, schoolID string, req *models.CreateCategoryRequest) (*models.CategoryResponse, error) {
	cat, err := s.categoryRepo.Create(ctx, schoolID, req.Name)
	if err != nil {
		// The DB unique constraint on (school_id, name) produces a specific
		// error code (23505) when violated. We check for it here and return
		// a clean error rather than leaking a raw Postgres message to the client.
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
		Metadata: map[string]string{"name": cat.Name},
	})

	return toCategoryResponse(cat), nil
}

// GetAll returns all categories for a school, ordered alphabetically.
// Always returns a slice (never nil) so the API response is always
// a JSON array, even when the school has no categories yet.
func (s *CategoryService) GetAll(ctx context.Context, schoolID string) ([]*models.CategoryResponse, error) {
	categories, err := s.categoryRepo.GetAllBySchool(ctx, schoolID)
	if err != nil {
		return nil, fmt.Errorf("get categories: %w", err)
	}

	// Convert each domain model to a response struct, stripping fields
	// the client does not need (school_id is implicit from the JWT).
	responses := make([]*models.CategoryResponse, len(categories))
	for i, cat := range categories {
		responses[i] = toCategoryResponse(cat)
	}

	return responses, nil
}

// Delete removes a category from a school's library.
// Will return ErrCategoryNotEmpty if the category still contains books,
// and ErrNotFound if the category does not exist or belongs to a
// different school.
func (s *CategoryService) Delete(ctx context.Context, schoolID, categoryID string) error {
	err := s.categoryRepo.Delete(ctx, categoryID, schoolID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		// The DB RESTRICT constraint on books.category_id produces error
		// code 23503 (foreign key violation) when books still exist.
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
		Name:      c.Name,
		CreatedAt: c.CreatedAt,
	}
}

// =============================================================
// POSTGRES ERROR HELPERS
// Centralised here so the same checks can be reused across services
// without importing the postgres driver into every service file.
// We check error message substrings because lib/pq error codes
// require a type assertion to pq.Error — keeping it simple here
// avoids coupling the service layer to a specific DB driver.
// =============================================================

// isUniqueViolation returns true when a DB error is caused by a
// UNIQUE constraint violation (Postgres error code 23505).
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23505")
}

// isForeignKeyViolation returns true when a DB error is caused by a
// FOREIGN KEY constraint violation (Postgres error code 23503).
// This occurs when trying to delete a category that still has books.
func isForeignKeyViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23503")
}