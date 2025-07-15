package handlers

import (
    "net/http"
)

// HealthHandler handles HTTP requests to the /health endpoint
func HealthHandler(w http.ResponseWriter, r *http.Request) {
    // Only allow GET method
    if r.Method != http.MethodGet {
        w.WriteHeader(http.StatusMethodNotAllowed)
        return
    }

    // Set content type header
    w.Header().Set("Content-Type", "text/plain")
    
    // Write 200 OK status and "OK" response
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("OK"))
}
