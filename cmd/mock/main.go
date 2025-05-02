package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

func startMockServer(port string, delay time.Duration) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		fmt.Fprintf(w, "Response from Server %s (delay: %v)", port, delay)
	})

	log.Printf("Mock server started on :%s (delay: %v)", port, delay)
	go func() {
		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Fatalf("Failed to start mock server on :%s: %v", port, err)
		}
	}()
}

func main() {
	startMockServer("8081", 1*time.Second)
	startMockServer("8082", 2*time.Second)
	startMockServer("8083", 3*time.Second)
	select {}
}
