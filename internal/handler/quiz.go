package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/middleware"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/service"
)

// QuizHandler handles HTTP requests for quiz questions, attempts,
// teaching dialogue audio, and grade progress.
type QuizHandler struct {
	quizService *service.QuizService
}

// NewQuizHandler creates a QuizHandler with its dependency injected.
func NewQuizHandler(quizService *service.QuizService) *QuizHandler {
	return &QuizHandler{quizService: quizService}
}

// GetQuestions handles GET /student/books/:id/questions.
// Returns all questions for a book's quiz without the correct answers.
// Only available to grade-role tokens — students cannot see answers
// before submitting.
func (h *QuizHandler) GetQuestions(c *gin.Context) {
	gradeID := middleware.GradeIDFromContext(c)
	bookID  := c.Param("id")

	if bookID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "book id is required"})
		return
	}

	questions, err := h.quizService.GetQuestions(c.Request.Context(), gradeID, bookID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "book not found"})
		case errors.Is(err, service.ErrQuestionsNotReady):
			c.JSON(http.StatusAccepted, models.ErrorResponse{Error: err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to fetch questions"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"questions": questions,
		"count":     len(questions),
	})
}

// SubmitAttempt handles POST /student/books/:id/attempts.
// Records a completed quiz and returns the results including
// correct answers so the teacher can review with the student.
func (h *QuizHandler) SubmitAttempt(c *gin.Context) {
	//Read preview flag admin previews are scored but not saved
	preview := c.Query("preview") == "true"

	gradeID := middleware.GradeIDFromContext(c)
	bookID  := c.Param("id")

	if bookID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "book id is required"})
		return
	}

	var req models.SubmitAttemptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	result, err := h.quizService.SubmitAttempt(c.Request.Context(), gradeID, bookID, &req, preview)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "book not found"})
		case errors.Is(err, service.ErrWrongAnswerCount):
			c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to save attempt"})
		}
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetDialogueURL handles GET /student/books/:id/dialogue.
// Returns a presigned URL for the book's two-voice teaching dialogue.
// Returns 202 Accepted if dialogue generation is still in progress.
func (h *QuizHandler) GetDialogueURL(c *gin.Context) {
	gradeID := middleware.GradeIDFromContext(c)
	bookID  := c.Param("id")

	if bookID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "book id is required"})
		return
	}

	url, err := h.quizService.GetDialogueURL(c.Request.Context(), gradeID, bookID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotFound):
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "book not found"})
		case errors.Is(err, service.ErrQuestionsNotReady):
			// 202 tells the frontend to try again later — dialogue is
			// still being generated in the background.
			c.JSON(http.StatusAccepted, models.ErrorResponse{Error: "teaching dialogue is not ready yet"})
		default:
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to get dialogue"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"dialogue_url": url})
}

// GetGradeProgress handles GET /admin/grades/:id/progress.
// Returns a quiz progress summary for a grade — how many attempts,
// best scores, and average scores per book. Admin only.
func (h *QuizHandler) GetGradeProgress(c *gin.Context) {
	schoolID := middleware.SchoolIDFromContext(c)
	gradeID  := c.Param("id")

	if gradeID == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "grade id is required"})
		return
	}

	progress, err := h.quizService.GetGradeProgress(c.Request.Context(), schoolID, gradeID)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, models.ErrorResponse{Error: "grade not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: "failed to fetch progress"})
		return
	}

	c.JSON(http.StatusOK, progress)
}
