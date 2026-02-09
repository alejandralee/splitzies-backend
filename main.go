package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"splitzies/persistence"
	"splitzies/storage"
	"splitzies/transport"
)

func main() {
	ctx := context.Background()
	// Initialize database
	if err := persistence.InitDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer persistence.CloseDB()

	// Run migrations
	if err := persistence.RunMigrations("migrations"); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	fmt.Println("Database initialized successfully")

	port := os.Getenv("PORT")
	if port == "" {
		// Default for local dev; Heroku provides PORT.
		port = "8080"
	}
	addr := ":" + port

	// Initialize Google Cloud Storage client
	gcsClient, err := storage.NewGCSClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create GCS client: %v", err)
	}
	defer gcsClient.Close()

	// Initialize Vision client
	visionClient, err := storage.NewVisionClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create Vision client: %v", err)
	}
	defer visionClient.Close()

	transport := transport.NewTransport(gcsClient, visionClient)

	// http.HandleFunc("/receipts", transport.AddReceiptHandler)
	http.HandleFunc("/receipts/image", transport.UploadReceiptImageHandler)

	fmt.Printf("Server starting on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
