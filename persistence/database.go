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

// Client wraps the database connection for use by handlers.
type Client struct {
	db *pgx.Conn
}

// NewClient creates a new persistence client and connects to the database.
func NewClient(ctx context.Context, databaseURL string) (*Client, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable is required")
	}

	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the database: %w", err)
	}

	// Set global DB for receipt.go (SaveReceipt) which uses package-level DB
	DB = conn

	var version string
	if err := conn.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		conn.Close(ctx)
		return nil, fmt.Errorf("query failed: %w", err)
	}

	log.Printf("Connected to: %s\n", version)
	return &Client{db: conn}, nil
}

// Close closes the database connection.
func (c *Client) Close(ctx context.Context) error {
	if c.db != nil {
		DB = nil
		return c.db.Close(ctx)
	}
	return nil
}

// RunMigrations runs all pending database migrations using goose.
func (c *Client) RunMigrations(ctx context.Context, migrationsDir string) error {
	if c.db == nil {
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
