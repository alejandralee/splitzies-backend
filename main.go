package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"splitzies/persistence"
	"splitzies/storage"
	"splitzies/transport"
)

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

	fmt.Printf("Server starting on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
