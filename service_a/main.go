package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	defaultPort        = "8080"
	shutdownTimeout    = 10 * time.Second
	serverReadTimeout  = 10 * time.Second
	serverWriteTimeout = 10 * time.Second
	serverIdleTimeout  = 60 * time.Second
)

var serviceBURL = ""
var cepRegex = regexp.MustCompile(`^\d{8}$`)

type CEPRequest struct {
	CEP string `json:"cep"`
}

type ErrorResponse struct {
	Message string `json:"message"`
}

type WeatherResponse struct {
	City  string  `json:"city"`
	TempC float64 `json:"temp_C"`
	TempF float64 `json:"temp_F"`
	TempK float64 `json:"temp_K"`
}

type SuccessResponse struct {
	City  string  `json:"city"`
	TempC float64 `json:"temp_C"`
	TempF float64 `json:"temp_F"`
	TempK float64 `json:"temp_K"`
}

func writeJSON(w http.ResponseWriter, data interface{}, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Error encoding JSON: %v", err)
	}
}

func writeError(w http.ResponseWriter, msg string, code int) {
	writeJSON(w, ErrorResponse{Message: msg}, code)
}

func callServiceB(cep string) (*WeatherResponse, error) {
	log.Printf("Calling Service B with CEP: %s", cep)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(serviceBURL + "?cep=" + cep)
	if err != nil {
		log.Printf("Error calling service B: %v", err)
		return nil, err
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("cannot find zipcode")
	}
	defer resp.Body.Close()

	var weather WeatherResponse
	if err := json.NewDecoder(resp.Body).Decode(&weather); err != nil {
		return nil, err
	}

	return &weather, nil

}

func handleCEP(w http.ResponseWriter, r *http.Request) {
	var req CEPRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.CEP == "" {
		writeError(w, "cep is required", http.StatusBadRequest)
		return
	}

	if !isValidCEP(req.CEP) {
		writeError(w, "invalid zipcode", http.StatusUnprocessableEntity)
		return
	}

	log.Printf("Processing CEP: %s", req.CEP)

	weatherData, err := callServiceB(req.CEP)
	if err != nil {
		log.Printf("Error calling service B: %v", err)
		if err.Error() == "cannot find zipcode" {
			writeError(w, "can not find zipcode", http.StatusNotFound)
			return
		}
		writeError(w, "failed to get weather data", http.StatusInternalServerError)
		return
	}

	writeJSON(w, SuccessResponse{
		City:  weatherData.City,
		TempC: weatherData.TempC,
		TempF: weatherData.TempF,
		TempK: weatherData.TempK,
	}, http.StatusOK)
}

func isValidCEP(cep string) bool {
	return cepRegex.MatchString(cep)
}

func setupRouter() *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Post("/service-a", handleCEP)

	return r
}

func main() {
	serviceBURL = os.Getenv("SERVICE_B_URL")
	if serviceBURL == "" {
		log.Panic("SERVICE_B_URL environment variable not set")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	router := setupRouter()

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
