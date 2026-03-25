package db

import (
	"database/sql"
	"fmt"
	"log"

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

	log.Println("Database Connection Successful")
	return db,nil

}
