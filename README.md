# splitzies-backend

## Setup

### Prerequisites

- Go 1.23.4 or later
- PostgreSQL 12 or later
- Git (for cloning the repository)

### Installation

1. Clone the repository:
   ```bash
   git clone <repository-url>
   cd splitzies-backend
   ```

2. Install dependencies:
   ```bash
   go mod download
   ```

3. Set up PostgreSQL database:
   
   **Local Development:**
   
   Create a local PostgreSQL database:
   ```bash
   createdb splitzies
   ```
   
   Or using `psql`:
   ```bash
   psql -U postgres
   CREATE DATABASE splitzies;
   \q
   ```
   
   Set the `DATABASE_URL` environment variable:
   ```bash
   export DATABASE_URL="postgresql://postgres:password@localhost:5432/splitzies?sslmode=disable"
   ```
   
   Replace `postgres` and `password` with your PostgreSQL username and password.
   
   **Production (Supabase):**
   
   Set the `DATABASE_URL` environment variable with your Supabase connection string:
   ```bash
   export DATABASE_URL="postgresql://postgres:[YOUR-PASSWORD]@db.ymkgstdgbfuoaabkctcj.supabase.co:5432/postgres"
   ```
   
   Replace `[YOUR-PASSWORD]` with your actual Supabase database password.

## Running

### Start the server

Make sure the `DATABASE_URL` environment variable is set, then run:

```bash
go run main.go
```

The server will start on port `8080`. You should see:
```
Database initialized successfully
Server starting on :8080
```

### Build and run

Alternatively, you can build the application first and then run the binary:

```bash
go build -o splitzies
./splitzies
```

## Database Migrations

The application uses [goose](https://github.com/pressly/goose) for database migrations. Migrations are automatically run when the application starts.

### Running migrations manually

You can also run migrations manually using the goose CLI:

```bash
# Install goose CLI
go install github.com/pressly/goose/v3/cmd/goose@latest

# Run migrations
goose -dir migrations postgres "$DATABASE_URL" up

# Rollback last migration
goose -dir migrations postgres "$DATABASE_URL" down
```

### Creating new migrations

Create a new migration file:

```bash
goose -dir migrations create migration_name sql
```

This will create two files:
- `migrations/XXXXXX_migration_name.up.sql` - Migration to apply
- `migrations/XXXXXX_migration_name.down.sql` - Migration to rollback

## API Endpoints

- `GET /` - Hello world endpoint
- `POST /receipts` - Add a receipt
- `POST /receipts/image` - Upload a receipt image (Vision OCR)
- `POST /receipts/document-ai` - Upload a receipt image/PDF (Document AI receipt processor)

## Database

The application uses PostgreSQL for both local development and production. The database schema is managed through migrations in the `migrations/` directory. Migrations are automatically applied when the application starts.
