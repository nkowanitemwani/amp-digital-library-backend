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
	// InitializeDatabase returns a *sql.DB with connection pool
	// settings already configured (MaxOpenConns, MaxIdleConns, etc).
	// ==========================================================
	database, err := db.InitializeDatabase(cfg)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer database.Close()

	// ==========================================================
	// STORAGE
	// Switch NewLocalStorage for NewS3Storage here when deploying.
	// Everything above this line is unaffected by that change.
	// ==========================================================
	store, err := storage.NewLocalStorage("./storage/files", "http://localhost:"+cfg.ServerPort+"/files")
	if err != nil {
		log.Fatalf("failed to initialise storage: %v", err)
	}

	// ==========================================================
	// REPOSITORIES
	// Each repo receives the shared DB pool.
	// Repos are the only layer that holds a DB reference.
	// ==========================================================
	schoolRepo   := repository.NewSchoolRepository(database)
	categoryRepo := repository.NewCategoryRepository(database)
	bookRepo     := repository.NewBookRepository(database)
	auditRepo    := repository.NewAuditRepository(database)

	// ==========================================================
	// SERVICES
	// Each service receives only the repos and dependencies it needs.
	// Services never hold a DB reference directly.
	// ==========================================================
	schoolService   := service.NewSchoolService(schoolRepo, auditRepo, cfg.JWTSecret)
	categoryService := service.NewCategoryService(categoryRepo, auditRepo)
	bookService     := service.NewBookService(bookRepo, categoryRepo, auditRepo, store)

	// ==========================================================
	// PROCESSOR
	// The background worker pool that converts PDFs to audio.
	// Started with a cancellable context so workers shut down
	// cleanly when the server receives a termination signal.
	// ==========================================================
	processor, err := service.NewProcessor(
		bookRepo,
		auditRepo,
		store,
		cfg.AWSRegion,
		cfg.AWSAccessKeyId,
		cfg.AWSSecretAccessKey,
		cfg.ProcessorWorkers,
		cfg.ProcessorPollSecs,
	)
	if err != nil {
		log.Fatalf("failed to initialise processor: %v", err)
	}

	// processorCtx is cancelled on shutdown — this is what stops
	// the worker goroutines gracefully (see processor.go runWorker).
	processorCtx, stopProcessor := context.WithCancel(context.Background())
	processor.Start(processorCtx)

	// ==========================================================
	// HANDLERS
	// Each handler receives only the service it needs.
	// Handlers never hold repo or DB references.
	// ==========================================================
	schoolHandler   := handler.NewSchoolHandler(schoolService)
	categoryHandler := handler.NewCategoryHandler(categoryService)
	bookHandler     := handler.NewBookHandler(bookService)

	// ==========================================================
	// ROUTER
	// ==========================================================
	router := gin.Default()

	// CORS — allow all origins in development.
	// Restrict AllowOrigins to specific domains before deploying.
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false, // must be false when AllowOrigins is "*"
		MaxAge:           12 * time.Hour,
	}))

	// Serve local storage files under /files so audio URLs resolve.
	// In production with S3 this line is removed — S3 URLs are self-contained.
	router.Static("/files", "./storage/files")

	// -- Public routes (no JWT required) --
	auth := router.Group("/auth")
	{
		auth.POST("/register", schoolHandler.Register)
		auth.POST("/login",    schoolHandler.Login)
	}

	// -- Protected routes (JWT required) --
	// RequireAuth validates the token and injects school_id into the context.
	// Every handler inside this group reads school_id from the context —
	// never from the request body.
	protected := router.Group("/")
	protected.Use(middleware.RequireAuth(schoolService))
	{
		// School profile
		protected.GET("/auth/me", schoolHandler.Me)

		// Categories
		protected.POST("/categories",      categoryHandler.Create)
		protected.GET("/categories",       categoryHandler.GetAll)
		protected.DELETE("/categories/:id", categoryHandler.Delete)

		// Books — upload and manage
		protected.POST("/books",           bookHandler.Upload)
		protected.GET("/books/:id",        bookHandler.GetByID)
		protected.DELETE("/books/:id",     bookHandler.Delete)

		// Books — list by category (primary student path)
		// Nested under /categories so the URL reflects the relationship:
		// "give me all books in this category"
		protected.GET("/categories/:id/books", bookHandler.GetByCategory)
	}

	// ==========================================================
	// SERVER — with graceful shutdown
	// Rather than router.Run() (which blocks and cannot be stopped
	// cleanly), we use http.Server so we can intercept OS signals
	// and shut down without dropping in-flight requests.
	// ==========================================================
	server := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: router,

		// Timeouts prevent slow or malicious clients from holding
		// connections open indefinitely and exhausting the server.
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second, // higher than ReadTimeout to allow file uploads
		IdleTimeout:  60 * time.Second,
	}

	// Start the server in a goroutine so the main goroutine can
	// block on the signal channel below.
	go func() {
		log.Printf("server starting on port %s", cfg.ServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// ==========================================================
	// GRACEFUL SHUTDOWN
	// Block until we receive SIGINT (Ctrl+C) or SIGTERM (Docker stop,
	// Kubernetes pod termination). Then stop the processor workers
	// and give in-flight HTTP requests 10 seconds to complete.
	// ==========================================================
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit // blocks here until a signal arrives

	log.Println("shutdown signal received — stopping gracefully")

	// Stop processor workers first so they do not pick up new jobs
	// while we are waiting for HTTP requests to drain.
	stopProcessor()
	log.Println("processor workers stopped")

	// Give in-flight HTTP requests 10 seconds to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server forced to shut down: %v", err)
	}

	log.Println("server stopped cleanly")
}