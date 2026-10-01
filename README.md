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
- `POST /receipts/image` - Upload a receipt image (parsed by Gemini)

## Receipt parsing

`POST /receipts/image` sends the uploaded image straight to Gemini on Vertex AI,
which reads the receipt and returns structured items, tax, tip, title, currency
and date in one call. Keeping the image intact preserves the column layout that
tells us which price belongs to which line item.

If that call fails or returns no items, the request automatically falls back to
the older pipeline: Cloud Vision `DOCUMENT_TEXT_DETECTION`, then Gemini over the
extracted text, then a regex parser as a last resort.

Configuration:

| Variable | Default | Purpose |
| --- | --- | --- |
| `GEMINI_MODEL` | `gemini-3.1-flash-lite` | Model used for parsing. Set to `gemini-2.5-flash` to restore the previous model. |
| `RECEIPT_PARSE_MODE` | `image` | Set to `ocr` to skip the image path entirely and use only the Cloud Vision OCR pipeline. |
| `VERTEX_AI_LOCATION` | `global` | Vertex AI region. |
| `GCP_PROJECT_ID` | — | Required (falls back to `GOOGLE_CLOUD_PROJECT`). |
| `GOOGLE_APPLICATION_CREDENTIALS_JSON` | — | Required service account JSON, used for Vertex AI, Vision and GCS. |

### Rolling back

Both switches apply on restart, no deploy needed:

```bash
# Full rollback to the previous Vision OCR + Gemini 2.5 Flash behaviour
heroku config:set RECEIPT_PARSE_MODE=ocr GEMINI_MODEL=gemini-2.5-flash

# Keep the image path, but on the older model
heroku config:set GEMINI_MODEL=gemini-2.5-flash

# Back to the default
heroku config:unset RECEIPT_PARSE_MODE GEMINI_MODEL
```

The startup log line `receipt parsing configured mode=... model=...` confirms
which pipeline a running dyno is using, and every upload logs the model plus
whether it fell back.

## Database

The application uses PostgreSQL for both local development and production. The database schema is managed through migrations in the `migrations/` directory. Migrations are automatically applied when the application starts.
