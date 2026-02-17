package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Handler struct {
	ServiceBURL string
}

func NewHandler(serviceBURL string) *Handler {
	return &Handler{ServiceBURL: serviceBURL}
}

func (h *Handler) callServiceB(cep string) (*WeatherResponse, error) {
	log.Printf("Calling Service B with CEP: %s", cep)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(h.ServiceBURL + "?cep=" + cep)
	if err != nil {
		log.Printf("Error calling service B: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("cannot find zipcode")
	}

	var weather WeatherResponse
	if err := json.NewDecoder(resp.Body).Decode(&weather); err != nil {
		return nil, err
	}

	return &weather, nil
}

func (h *Handler) HandleCEP(w http.ResponseWriter, r *http.Request) {
	var req CEPRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.CEP == "" {
		WriteError(w, "cep is required", http.StatusBadRequest)
		return
	}

	if !IsValidCEP(req.CEP) {
		WriteError(w, "invalid zipcode", http.StatusUnprocessableEntity)
		return
	}

	log.Printf("Processing CEP: %s", req.CEP)

	weatherData, err := h.callServiceB(req.CEP)
	if err != nil {
		log.Printf("Error calling service B: %v", err)
		if err.Error() == "cannot find zipcode" {
			WriteError(w, "can not find zipcode", http.StatusNotFound)
			return
		}
		WriteError(w, "failed to get weather data", http.StatusInternalServerError)
		return
	}

	WriteJSON(w, WeatherResponse{
		City:  weatherData.City,
		TempC: weatherData.TempC,
		TempF: weatherData.TempF,
		TempK: weatherData.TempK,
	}, http.StatusOK)
}

func SetupRouter(h *Handler) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Post("/service-a", h.HandleCEP)

	return r
}
