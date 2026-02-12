package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"splitzies/persistence"
	"splitzies/storage"
	tr "splitzies/transport"
)

//go:embed swagger/docs.html swagger.yaml
var swaggerFS embed.FS

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

	httpTransport := tr.NewTransport(persistenceClient, gcsClient, visionClient)

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
	log.Fatal(http.ListenAndServe(addr, nil))
}
