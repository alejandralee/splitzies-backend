package persistence

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
)

var DB *pgx.Conn

// InitDB matches the simple Supabase sample: open a pgx connection and log the server version.
func InitDB() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL environment variable is required")
	}

	conn, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to the database: %w", err)
	}
	DB = conn

	// Example query to test connection
	var version string
	if err := DB.QueryRow(context.Background(), "SELECT version()").Scan(&version); err != nil {
		return fmt.Errorf("query failed: %w", err)
	}

	log.Printf("Connected to: %s\n", version)
	return nil
}

// CloseDB closes the database connection.
func CloseDB() error {
	if DB != nil {
		return DB.Close(context.Background())
	}
	return nil
}
