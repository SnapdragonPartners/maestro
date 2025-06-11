package main

import (
	"fmt"
	"log"
	"net/http"
)

// handleHomepage is the handler function for the homepage route
func handleHomepage(w http.ResponseWriter, r *http.Request) {
	// Write the "Hello World" message to the response writer
	_, err := fmt.Fprint(w, "Hello World")
	if err != nil {
		// Handle the error case
		log.Printf("Error writing response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

func main() {
	// Create a new HTTP server
	server := &http.Server{
		Addr: ":8080", // Listen on port 8080
	}

	// Register the homepage handler
	http.HandleFunc("/", handleHomepage)

	// Start the server
	log.Println("Starting server on :8080")
	err := server.ListenAndServe()
	if err != nil {
		// Handle the error case
		log.Fatalf("Error starting server: %v", err)
	}
}
