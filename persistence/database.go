package persistence

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Client wraps a connection pool for safe concurrent use and automatic reconnection.
type Client struct {
	db *pgxpool.Pool
}

// NewClient creates a connection pool and verifies connectivity.
func NewClient(ctx context.Context, databaseURL string) (*Client, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Verify the pool can reach the database.
	var version string
	if err := pool.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Printf("Connected to: %s", version)
	return &Client{db: pool}, nil
}

// Ping verifies the database connection is alive.
func (c *Client) Ping(ctx context.Context) error {
	return c.db.Ping(ctx)
}

// Close drains and closes all pool connections.
func (c *Client) Close(_ context.Context) error {
	if c.db != nil {
		c.db.Close()
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
