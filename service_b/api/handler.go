package api

import (
	"context"
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
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
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
	carrier := propagation.HeaderCarrier(r.Header)
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), carrier)
	tracer := otel.Tracer("service-b")

	ctx, span := tracer.Start(ctx, "service-b: handle-weather")
	defer span.End()

	cep := r.URL.Query().Get("cep")
	log.Printf("Request recebido: cep=%s, remote=%s", cep, r.RemoteAddr)

	if !IsValidCEP(cep) {
		log.Printf("Erro: CEP invalido: %s", cep)
		span.RecordError(fmt.Errorf("invalid zipcode: %s", cep))
		span.SetStatus(codes.Error, "invalid zipcode")
		WriteError(w, "invalid zipcode", http.StatusUnprocessableEntity)
		return
	}

	span.SetAttributes(attribute.String("cep", cep))

	city, err := h.getCityByCEP(ctx, cep)
	if err != nil {
		span.RecordError(err)
		if errors.Is(err, ErrNotFound) {
			log.Printf("Erro: CEP nao encontrado: %s", cep)
			span.SetStatus(codes.Error, "zipcode not found")
			WriteError(w, err.Error(), http.StatusNotFound)
		} else {
			log.Printf("Erro ao consultar ViaCEP: %v", err)
			span.SetStatus(codes.Error, "failed to get city by cep")
			WriteError(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	span.SetAttributes(attribute.String("city", city))

	tempC, err := h.getTempByCity(ctx, city)
	if err != nil {
		log.Printf("Erro ao consultar WeatherAPI para cidade %s: %v", city, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get temperature")
		WriteError(w, "internal error", http.StatusInternalServerError)
		return
	}

	tempF, tempK := h.convertTemperatures(ctx, tempC)

	resp := TempResponse{
		City:  city,
		TempC: tempC,
		TempF: tempF,
		TempK: tempK,
	}

	log.Printf("Resposta: cep=%s, cidade=%s, tempC=%.2f", cep, city, tempC)
	span.SetStatus(codes.Ok, "")
	WriteJSON(w, resp, http.StatusOK)
}

func (h *Handler) convertTemperatures(ctx context.Context, tempC float64) (float64, float64) {
	tracer := otel.Tracer("service-b")
	_, span := tracer.Start(ctx, "service-b: convert-temperatures")
	defer span.End()

	tempF := tempC*fahrenheitMultiplier + fahrenheitBase
	tempK := tempC + kelvinBase

	span.SetAttributes(
		attribute.Float64("temp_C", tempC),
		attribute.Float64("temp_F", tempF),
		attribute.Float64("temp_K", tempK),
	)
	span.SetStatus(codes.Ok, "")

	return tempF, tempK
}

func (h *Handler) getTempByCity(ctx context.Context, city string) (float64, error) {
	tracer := otel.Tracer("service-b")
	ctx, span := tracer.Start(ctx, "service-b: get-temp-by-city")
	defer span.End()

	span.SetAttributes(attribute.String("city", city))

	requestURL := fmt.Sprintf("https://api.weatherapi.com/v1/current.json?key=%s&q=%s", h.WeatherAPIKey, url.QueryEscape(city))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create request")
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "http request failed")
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to read response body")
		return 0, fmt.Errorf("failed to read weatherapi response body: %w", err)
	}

	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))

	if resp.StatusCode != 200 {
		err := fmt.Errorf("weatherapi error: %d - %s", resp.StatusCode, string(body))
		span.RecordError(err)
		span.SetStatus(codes.Error, "weatherapi returned error status")
		return 0, err
	}

	tempC, err := h.decodeWeatherResponse(ctx, body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to decode weather response")
		return 0, err
	}

	span.SetStatus(codes.Ok, "")
	return tempC, nil
}

func (h *Handler) decodeWeatherResponse(ctx context.Context, body []byte) (float64, error) {
	tracer := otel.Tracer("service-b")
	_, span := tracer.Start(ctx, "service-b: decode-weather-response")
	defer span.End()

	var weather WeatherAPIResponse
	if err := json.Unmarshal(body, &weather); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "json unmarshal failed")
		return 0, err
	}

	span.SetAttributes(attribute.Float64("temp_c", weather.Current.TempC))
	span.SetStatus(codes.Ok, "")
	return weather.Current.TempC, nil
}

func (h *Handler) getCityByCEP(ctx context.Context, cep string) (string, error) {
	tracer := otel.Tracer("service-b")
	ctx, span := tracer.Start(ctx, "service-b: get-city-by-cep")
	defer span.End()

	span.SetAttributes(attribute.String("cep", cep))

	requestURL := fmt.Sprintf("https://viacep.com.br/ws/%s/json/", cep)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create request")
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "http request failed")
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to read response body")
		return "", err
	}

	city, err := h.decodeViaCEPResponse(ctx, body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to decode viacep response")
		return "", err
	}

	span.SetAttributes(attribute.String("city", city))
	span.SetStatus(codes.Ok, "")
	return city, nil
}

func (h *Handler) decodeViaCEPResponse(ctx context.Context, body []byte) (string, error) {
	tracer := otel.Tracer("service-b")
	_, span := tracer.Start(ctx, "service-b: decode-viacep-response")
	defer span.End()

	var viaCEP ViaCEPResponse
	if err := json.Unmarshal(body, &viaCEP); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "json unmarshal failed")
		return "", err
	}

	if viaCEP.Error != "" || viaCEP.City == "" {
		span.RecordError(ErrNotFound)
		span.SetStatus(codes.Error, "zipcode not found")
		return "", ErrNotFound
	}

	span.SetAttributes(attribute.String("city", viaCEP.City))
	span.SetStatus(codes.Ok, "")
	return viaCEP.City, nil
}

func SetupRouter(h *Handler) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/weather", h.WeatherHandler)

	return otelhttp.NewHandler(r, "service-b-server")
}
