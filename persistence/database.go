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

// Client wraps the database connection.
type Client struct {
	db *pgx.Conn
}

// NewClient connects to the database and returns a Client.
func NewClient(ctx context.Context, databaseURL string) (*Client, error) {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	var version string
	if err := conn.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		conn.Close(ctx)
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Printf("Connected to: %s", version)
	return &Client{db: conn}, nil
}

// Close closes the database connection.
func (c *Client) Close(ctx context.Context) error {
	if c.db != nil {
		return c.db.Close(ctx)
	}
	return nil
}

// RunMigrations runs all pending database migrations using goose.
func (c *Client) RunMigrations(ctx context.Context, migrationsDir string) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL environment variable is required")
	}

	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse database URL: %w", err)
	}

	sqlDB := stdlib.OpenDB(*config)
	defer sqlDB.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS(migrationsDir))
	if err != nil {
		return fmt.Errorf("failed to create goose provider: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	if len(results) > 0 {
		log.Printf("Applied %d migration(s)", len(results))
		for _, r := range results {
			if r.Source != nil {
				log.Printf("  - Migration %d: %s", r.Source.Version, r.Source.Path)
			}
		}
	} else {
		log.Println("No new migrations to apply")
	}

	return nil
}
