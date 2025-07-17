package main

import (
	"log"
	"net/http"
)

func main() {
	// Create a new router
	mux := http.NewServeMux()

	// Register the health check endpoint
	mux.HandleFunc("/health", handleHealth)

	// Start the server
	log.Println("Server starting on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}

// handleHealth handles the /health endpoint
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
