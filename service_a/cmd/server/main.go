package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/carlosfiori/pos-go-fullcycle-desafio-otel/service_a/api"
)

const (
	defaultPort        = "8080"
	shutdownTimeout    = 10 * time.Second
	serverReadTimeout  = 10 * time.Second
	serverWriteTimeout = 10 * time.Second
	serverIdleTimeout  = 60 * time.Second
)

func main() {
	serviceBURL := os.Getenv("SERVICE_B_URL")
	if serviceBURL == "" {
		log.Panic("SERVICE_B_URL environment variable not set")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	handler := api.NewHandler(serviceBURL)
	router := api.SetupRouter(handler)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
		IdleTimeout:  serverIdleTimeout,
	}

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("Service A starting on port %s", port)
		serverErrors <- server.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		log.Fatalf("Error starting server: %v", err)
	case sig := <-shutdown:
		log.Printf("Received signal %v, shutting down gracefully...", sig)

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Error during shutdown: %v", err)
			server.Close()
		}

		log.Println("Service A stopped")
	}
}
