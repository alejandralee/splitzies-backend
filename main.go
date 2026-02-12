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
	"splitzies/transport"
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

	transport := transport.NewTransport(persistenceClient, gcsClient, visionClient)

	http.HandleFunc("/receipts/image", transport.UploadReceiptImageHandler)

	http.HandleFunc("/receipts/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if strings.HasSuffix(path, "/users") && r.Method == http.MethodPost {
			transport.AddUserToReceiptHandler(w, r)
			return
		}

		if strings.HasSuffix(path, "/items") && r.Method == http.MethodPost {
			transport.AssignItemsToUserHandler(w, r)
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
