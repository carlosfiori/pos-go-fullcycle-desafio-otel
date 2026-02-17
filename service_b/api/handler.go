package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	fahrenheitMultiplier = 1.8
	fahrenheitBase       = 32
	kelvinBase           = 273.15
)

var ErrNotFound = errors.New("can not find zipcode")

type Handler struct {
	WeatherAPIKey string
	HTTPClient    HTTPClient
}

func NewHandler(weatherAPIKey string, httpClient HTTPClient) *Handler {
	return &Handler{
		WeatherAPIKey: weatherAPIKey,
		HTTPClient:    httpClient,
	}
}

func (h *Handler) WeatherHandler(w http.ResponseWriter, r *http.Request) {
	cep := r.URL.Query().Get("cep")
	log.Printf("Request recebido: cep=%s, remote=%s", cep, r.RemoteAddr)

	if !IsValidCEP(cep) {
		log.Printf("Erro: CEP invalido: %s", cep)
		WriteError(w, "invalid zipcode", http.StatusUnprocessableEntity)
		return
	}

	city, err := h.getCityByCEP(cep)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			log.Printf("Erro: CEP nao encontrado: %s", cep)
			WriteError(w, err.Error(), http.StatusNotFound)
		} else {
			log.Printf("Erro ao consultar ViaCEP: %v", err)
			WriteError(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	tempC, err := h.getTempByCity(city)
	if err != nil {
		log.Printf("Erro ao consultar WeatherAPI para cidade %s: %v", city, err)
		WriteError(w, "internal error", http.StatusInternalServerError)
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
	WriteJSON(w, resp, http.StatusOK)
}

func (h *Handler) getTempByCity(city string) (float64, error) {
	requestURL := fmt.Sprintf("https://api.weatherapi.com/v1/current.json?key=%s&q=%s", h.WeatherAPIKey, url.QueryEscape(city))
	resp, err := h.HTTPClient.Get(requestURL)
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

func (h *Handler) getCityByCEP(cep string) (string, error) {
	requestURL := fmt.Sprintf("https://viacep.com.br/ws/%s/json/", cep)
	resp, err := h.HTTPClient.Get(requestURL)
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

func SetupRouter(h *Handler) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/weather", h.WeatherHandler)

	return r
}
