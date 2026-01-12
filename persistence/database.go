package persistence

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
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

// RunMigrations runs all pending database migrations using goose.
func RunMigrations(migrationsDir string) error {
	if DB == nil {
		return fmt.Errorf("database connection not initialized")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL environment variable is required")
	}

	// Convert pgx connection to *sql.DB for goose
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Create a *sql.DB using pgx stdlib driver
	sqlDB := stdlib.OpenDB(*config)
	defer sqlDB.Close()

	// Create filesystem from migrations directory
	migrationsFS := os.DirFS(migrationsDir)

	// Use the Provider API which properly handles .up.sql and .down.sql pairing
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrationsFS)
	if err != nil {
		return fmt.Errorf("failed to create goose provider: %w", err)
	}

	ctx := context.Background()

	// Run migrations up
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	if len(results) > 0 {
		log.Printf("Applied %d migration(s)", len(results))
		for _, result := range results {
			if result.Source != nil {
				log.Printf("  - Migration %d: %s", result.Source.Version, result.Source.Path)
			}
		}
	} else {
		log.Println("No new migrations to apply")
	}

	return nil
}
