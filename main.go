package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"splitzies/persistence"
	"splitzies/storage"
	tr "splitzies/transport"
)

//go:embed swagger/docs.html swagger.yaml
var swaggerFS embed.FS

func corsMiddleware(next http.Handler) http.Handler {
	// Keep this intentionally small + explicit: allow known frontend origins and dev localhost.
	// You can additionally extend via CORS_ALLOWED_ORIGINS (comma-separated list).
	allowedOrigins := map[string]struct{}{
		"http://localhost:3000": {},
		"http://localhost:5173": {},

		"https://preview-sandbox--69b99817fffd276b869a4db1.base44.app": {},
		// Base44 production domain (main app)
		"https://splitzies.base44.app": {},
	}

	if extra := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS")); extra != "" {
		for _, o := range strings.Split(extra, ",") {
			o = strings.TrimSpace(o)
			if o == "" {
				continue
			}
			allowedOrigins[o] = struct{}{}
		}
	}

	allowedMethods := "GET,POST,PATCH,OPTIONS"
	allowedHeaders := "Content-Type,Authorization"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if _, ok := allowedOrigins[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
				w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
			} else if u, err := url.Parse(origin); err == nil && u.Scheme != "" && u.Host != "" {
				// If a browser sends an origin we don't recognize, fail CORS by omitting headers.
				// (We still allow non-browser server-to-server calls which typically omit Origin.)
			}
		}

		if r.Method == http.MethodOptions {
			// Preflight: if origin isn't allowed, we intentionally return without CORS headers.
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func main() {
	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatalf("DATABASE_URL environment variable is required")
	}

	persistenceClient, err := persistence.NewClient(ctx, databaseURL)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer persistenceClient.Close(ctx)

	if err := persistenceClient.RunMigrations(ctx, "migrations"); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	fmt.Println("Database initialized successfully")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	gcsClient, err := storage.NewGCSClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create GCS client: %v", err)
	}
	defer gcsClient.Close()

	visionClient, err := storage.NewVisionClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create Vision client: %v", err)
	}
	defer visionClient.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	httpTransport := tr.NewTransport(logger, persistenceClient, gcsClient, visionClient)

	http.HandleFunc("/receipts/image", httpTransport.UploadReceiptImageHandler)

	http.HandleFunc("/receipts/", func(w http.ResponseWriter, r *http.Request) {
		pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

		// POST /receipts/{receipt_id}/users/{user_id}/items - assign items to user
		if len(pathParts) == 5 && pathParts[0] == "receipts" && pathParts[2] == "users" && pathParts[4] == "items" && r.Method == http.MethodPost {
			httpTransport.AssignItemsToUserHandler(w, r)
			return
		}

		// /receipts/{receipt_id}/users - GET or POST
		if len(pathParts) == 3 && pathParts[0] == "receipts" && pathParts[2] == "users" {
			if r.Method == http.MethodPost {
				httpTransport.AddUserToReceiptHandler(w, r)
				return
			}
			if r.Method == http.MethodGet {
				httpTransport.GetReceiptUsersHandler(w, r)
				return
			}
			http.Error(w, tr.NewInvalidMethodError(r.Method).Error(), http.StatusMethodNotAllowed)
			return
		}

		// GET /receipts/{receipt_id}/items
		if len(pathParts) == 3 && pathParts[0] == "receipts" && pathParts[2] == "items" && r.Method == http.MethodGet {
			httpTransport.GetReceiptItemsHandler(w, r)
			return
		}

		// GET /receipts/{receipt_id} - full receipt with users, items, assignments
		if len(pathParts) == 2 && pathParts[0] == "receipts" && r.Method == http.MethodGet {
			httpTransport.GetReceiptHandler(w, r)
			return
		}

		// PATCH /receipts/{receipt_id} - update tax/tip (when not parsed from receipt)
		if len(pathParts) == 2 && pathParts[0] == "receipts" && r.Method == http.MethodPatch {
			httpTransport.PatchReceiptHandler(w, r)
			return
		}

		http.NotFound(w, r)
	})

	// Swagger UI - docs.html loads the OpenAPI spec from /swagger.yaml
	http.HandleFunc("/swagger/docs.html", func(w http.ResponseWriter, r *http.Request) {
		data, _ := fs.ReadFile(swaggerFS, "swagger/docs.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})
	http.HandleFunc("/swagger.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-yaml")
		data, _ := fs.ReadFile(swaggerFS, "swagger.yaml")
		w.Write(data)
	})
	http.HandleFunc("/swagger", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/swagger/docs.html", http.StatusFound)
	})

	fmt.Printf("Server starting on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, corsMiddleware(http.DefaultServeMux)))
}
