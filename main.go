package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"splitzies/persistence"
	"splitzies/transport"
)

func main() {
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

	http.HandleFunc("/", transport.HelloWorldHandler)
	http.HandleFunc("/receipts", transport.AddReceiptHandler)
	http.HandleFunc("/receipts/image", transport.UploadReceiptImageHandler)
	http.HandleFunc("/receipts/document-ai", transport.UploadReceiptDocumentAIHandler)

	fmt.Printf("Server starting on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
