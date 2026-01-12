package main

import (
	"fmt"
	"log"
	"net/http"

	"splitzies/persistence"
	"splitzies/transport"
)

func main() {
	// Initialize database
	if err := persistence.InitDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer persistence.CloseDB()

	fmt.Println("Database initialized successfully")

	http.HandleFunc("/", transport.HelloWorldHandler)
	http.HandleFunc("/receipts", transport.AddReceiptHandler)

	fmt.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
