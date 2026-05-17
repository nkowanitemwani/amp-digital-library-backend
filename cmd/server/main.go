package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/config"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/db"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/handler"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/middleware"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/repository"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/service"
	"github.com/nkowanitemwani/amp-digital-library-backend/storage"
)

func main() {
	// ==========================================================
	// CONFIG
	// All environment variables are read once here.
	// Nothing else in the codebase calls os.Getenv directly.
	// ==========================================================
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// ==========================================================
	// DATABASE
	// Returns a *sql.DB with connection pool settings configured.
	// ==========================================================
	database, err := db.InitializeDatabase(cfg)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer database.Close()

	// ==========================================================
	// STORAGE
	// Swap NewLocalStorage for NewS3Storage here when deploying.
	// Everything above and below this line is unaffected.
	//	store, err := storage.NewLocalStorage(
	//	"./storage/files",
	//	"http://localhost:"+cfg.ServerPort+"/files",
	//	)
	//	if err != nil {
	//		log.Fatalf("failed to initialise storage: %v", err)
	//	}
	// ==========================================================
	store, err := storage.NewS3Storage(
		context.Background(),
		cfg.AWSRegion,
		cfg.S3Bucket,
		cfg.AWSAccessKeyId,
		cfg.AWSSecretAccessKey,
	)
	if err != nil {
		log.Fatalf("failed to initialise storage: %v", err)
	}

	// ==========================================================
	// REPOSITORIES
	// Each repo receives the shared DB pool.
	// Repos are the only layer that holds a DB reference.
	// ==========================================================
	schoolRepo := repository.NewSchoolRepository(database)
	gradeRepo := repository.NewGradeRepository(database)
	categoryRepo := repository.NewCategoryRepository(database)
	bookRepo := repository.NewBookRepository(database)
	auditRepo := repository.NewAuditRepository(database)
	questionRepo := repository.NewQuestionRepository(database)
	attemptRepo := repository.NewAttemptRepository(database)

	// ==========================================================
	// SERVICES
	// Each service receives only the repos and dependencies it needs.
	// Services never hold a DB reference directly.
	// ==========================================================
	schoolService := service.NewSchoolService(schoolRepo, auditRepo, cfg.JWTSecret)
	gradeService := service.NewGradeService(gradeRepo, auditRepo, schoolService)
	categoryService := service.NewCategoryService(categoryRepo, gradeRepo, auditRepo)
	bookService := service.NewBookService(bookRepo, categoryRepo, gradeRepo, auditRepo, store)
	quizService := service.NewQuizService(questionRepo, attemptRepo, bookRepo, gradeRepo, auditRepo, store)

	// ==========================================================
	// PROCESSOR
	// Background worker pool — converts uploaded PDFs to audio.
	// Cancelled on shutdown so workers exit cleanly.
	// ==========================================================
	processor, err := service.NewProcessor(
		bookRepo,
		questionRepo,
		auditRepo,
		store,
		cfg.ElevenLabsKey,
		cfg.ElevenLabsVoiceA,
		cfg.ElevenLabsVoiceB,
		cfg.GroqKey,
		cfg.ProcessorWorkers,
		cfg.ProcessorPollSecs,
	)
	if err != nil {
		log.Fatalf("failed to initialise processor: %v", err)
	}

	processorCtx, stopProcessor := context.WithCancel(context.Background())
	processor.Start(processorCtx)

	// ==========================================================
	// HANDLERS
	// Each handler receives only the service it needs.
	// Handlers never hold repo or DB references.
	// ==========================================================
	schoolHandler := handler.NewSchoolHandler(schoolService)
	gradeHandler := handler.NewGradeHandler(gradeService)
	categoryHandler := handler.NewCategoryHandler(categoryService)
	bookHandler := handler.NewBookHandler(bookService)
	quizHandler := handler.NewQuizHandler(quizService)

	// ==========================================================
	// ROUTER
	// ==========================================================
	router := gin.Default()

	// CORS — restrict AllowOrigins before deploying to production.
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))

	// Serve local storage files so audio URLs resolve in development.
	// Remove this line when deploying with S3 — S3 URLs are self-contained.
	// router.Static("/files", "./storage/files")

	// ── Public routes — no JWT required ──────────────────────
	auth := router.Group("/auth")
	{
		auth.POST("/register", schoolHandler.Register)
		auth.POST("/login", schoolHandler.Login)
		auth.POST("/grade/login", gradeHandler.Login)
	}

	// ── Admin routes — prefix /admin, role must be "admin" ───
	// All paths begin with /admin/ so they are structurally
	// separate from student routes — no suffix hacks needed.
	admin := router.Group("/admin")
	admin.Use(middleware.RequireAuth(schoolService), middleware.RequireAdmin)
	{
		// School profile
		admin.GET("/me", schoolHandler.Me)

		// Grade management
		admin.POST("/grades", gradeHandler.Create)
		admin.GET("/grades", gradeHandler.GetAll)
		admin.DELETE("/grades/:id", gradeHandler.Delete)

		// Category management — grade_id comes from request body / query param
		admin.POST("/categories", categoryHandler.Create)
		admin.GET("/grades/:id/categories", categoryHandler.GetAll)
		admin.DELETE("/categories/:id", categoryHandler.Delete)

		// Book management — grade_id comes from form fields / query param
		admin.POST("/books", bookHandler.Upload)
		admin.GET("/books/:id", bookHandler.GetByID)
		admin.DELETE("/books/:id", bookHandler.Delete)
		admin.GET("/categories/:id/books", bookHandler.GetByCategory)

		// Grade progress — teacher sees quiz scores per grade
		admin.GET("/grades/:id/progress", quizHandler.GetGradeProgress)
	}

	// ── Student routes — prefix /student, role must be "grade" ──
	student := router.Group("/student")
	student.Use(middleware.RequireAuth(schoolService), middleware.RequireGrade)
	{
		student.GET("/grades/:id/categories", categoryHandler.GetAll)
		student.GET("/categories/:id/books", bookHandler.GetByCategory)
		student.GET("/books/:id", bookHandler.GetByID)

		// Teaching dialogue — two-voice audio for a book
		student.GET("/books/:id/dialogue", quizHandler.GetDialogueURL)

		// Quiz — questions and attempt submission
		student.GET("/books/:id/questions", quizHandler.GetQuestions)
		student.POST("/books/:id/attempts", quizHandler.SubmitAttempt)
	}

	// ==========================================================
	// SERVER — with graceful shutdown
	// ==========================================================
	server := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: router,

		// Timeouts prevent slow or malicious clients from holding
		// connections open and exhausting the server.
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("server starting on port %s", cfg.ServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// ==========================================================
	// GRACEFUL SHUTDOWN
	// Block until SIGINT or SIGTERM, then stop processor workers
	// and give in-flight HTTP requests 10 seconds to complete.
	// ==========================================================
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutdown signal received — stopping gracefully")

	stopProcessor()
	log.Println("processor workers stopped")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server forced to shut down: %v", err)
	}

	log.Println("server stopped cleanly")
}
