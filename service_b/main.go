package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	defaultPort          = "8081"
	shutdownTimeout      = 10 * time.Second
	serverReadTimeout    = 10 * time.Second
	serverWriteTimeout   = 10 * time.Second
	serverIdleTimeout    = 60 * time.Second
	fahrenheitMultiplier = 1.8
	fahrenheitBase       = 32
	kelvinBase           = 273.15
)

type HTTPClient interface {
	Get(url string) (*http.Response, error)
}

var (
	cepRegex                 = regexp.MustCompile(`^\d{8}$`)
	ErrNotFound              = errors.New("can not find zipcode")
	httpClient    HTTPClient = &http.Client{Timeout: 5 * time.Second}
	weatherAPIKey string
)

type TempResponse struct {
	City  string  `json:"city"`
	TempC float64 `json:"temp_C"`
	TempF float64 `json:"temp_F"`
	TempK float64 `json:"temp_K"`
}

type ErrorResponse struct {
	Message string `json:"message"`
}

type ViaCEPResponse struct {
	City  string `json:"localidade"`
	Error string `json:"erro,omitempty"`
}

type WeatherAPIResponse struct {
	Current struct {
		TempC float64 `json:"temp_c"`
	} `json:"current"`
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

func weatherHandler(w http.ResponseWriter, r *http.Request) {
	cep := r.URL.Query().Get("cep")
	log.Printf("Request recebido: cep=%s, remote=%s", cep, r.RemoteAddr)

	if !isValidCEP(cep) {
		log.Printf("Erro: CEP inválido: %s", cep)
		writeError(w, "invalid zipcode", http.StatusUnprocessableEntity)
		return
	}

	city, err := getCityByCEP(httpClient, cep)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			log.Printf("Erro: CEP não encontrado: %s", cep)
			writeError(w, err.Error(), http.StatusNotFound)
		} else {
			log.Printf("Erro ao consultar ViaCEP: %v", err)
			writeError(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	tempC, err := getTempByCity(httpClient, city)
	if err != nil {
		log.Printf("Erro ao consultar WeatherAPI para cidade %s: %v", city, err)
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}

	tempF := tempC*fahrenheitMultiplier + fahrenheitBase
	tempK := tempC + kelvinBase

	resp := TempResponse{
		City:  city,
		TempC: tempC,
		TempF: tempF,
		TempK: tempK,
	}

	log.Printf("Resposta: cep=%s, cidade=%s, tempC=%.2f", cep, city, tempC)
	writeJSON(w, resp, http.StatusOK)
}

func isValidCEP(cep string) bool {
	return cepRegex.MatchString(cep)
}

func getTempByCity(client HTTPClient, city string) (float64, error) {
	requestURL := fmt.Sprintf("https://api.weatherapi.com/v1/current.json?key=%s&q=%s", weatherAPIKey, url.QueryEscape(city))
	resp, err := client.Get(requestURL)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read weatherapi response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("weatherapi error: %d - %s", resp.StatusCode, string(body))
	}

	var weather WeatherAPIResponse
	if err := json.Unmarshal(body, &weather); err != nil {
		return 0, err
	}
	return weather.Current.TempC, nil
}

func getCityByCEP(client HTTPClient, cep string) (string, error) {
	requestURL := fmt.Sprintf("https://viacep.com.br/ws/%s/json/", cep)
	resp, err := client.Get(requestURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var viaCEP ViaCEPResponse
	if err := json.Unmarshal(body, &viaCEP); err != nil {
		return "", err
	}

	if viaCEP.Error != "" || viaCEP.City == "" {
		return "", ErrNotFound
	}

	return viaCEP.City, nil
}

func setupRouter() *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/weather", weatherHandler)

	return r
}

func main() {
	weatherAPIKey = os.Getenv("WEATHERAPI_KEY")
	if weatherAPIKey == "" {
		weatherAPIKey = "54619d224b96477a9d420100262101"
	}
	if weatherAPIKey == "" {
		log.Panic("WEATHERAPI_KEY environment variable not set")
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
		log.Printf("Service B starting on port %s", port)
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

		log.Println("Service B stopped")
	}
}
