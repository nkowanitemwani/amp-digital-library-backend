package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/config"
)

var DB *sql.DB

func InitializeDatabase(cfg *config.Config) (*sql.DB,error) {

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName,
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("Failed to Open Database: %w", err)
	}

	if err = db.Ping(); err != nil {
		return nil,fmt.Errorf("Database Ping Failed: %w", err)
	}

	// Cap the number of open connections to avoid overwhelming Postgres.
	// Each connection is a process on the DB server — unlimited connections
	// under load will crash it. 25 is a safe starting point for a small app.
	db.SetMaxOpenConns(25)

	// Keep up to 10 idle connections warm so requests don't pay the cost
	// of opening a new connection on every query.
	db.SetMaxIdleConns(10)

	// Recycle connections after 1 hour to avoid using stale connections
	// that the DB server may have silently closed on its side.
	db.SetConnMaxLifetime(time.Hour)

	log.Println("Database Connection Successful")
	return db,nil

}
