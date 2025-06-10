```go
package main

import (
	"fmt"
	"log"
	"net/http"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	// Check if the server is healthy
	// You can add your custom health checks here
	healthy := true

	if healthy {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "Service Unavailable")
	}
}

func main() {
	http.HandleFunc("/health", healthHandler)

	// Start the HTTP server
	log.Println("Starting server on :8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
```

This code sets up a simple HTTP server with a `/health` endpoint that returns a 200 OK status code if the server is healthy, or a 503 Service Unavailable status code if it's not. The `healthHandler` function is where you can add your custom health checks.

In the `main` function, we register the `healthHandler` for the `/health` path using `http.HandleFunc`. Then, we start the HTTP server on port 8080 using `http.ListenAndServe`.

To run the server, save the code to a file (e.g., `server.go`) and execute:

```
go run server.go
```

You can then access the health endpoint at `http://localhost:8080/health`.

Note: This is a basic example, and in a production environment, you may want to add more robust health checks, error handling, and configuration options.