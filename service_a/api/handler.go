package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

type Handler struct {
	ServiceBURL string
}

func NewHandler(serviceBURL string) *Handler {
	return &Handler{ServiceBURL: serviceBURL}
}

func (h *Handler) callServiceB(ctx context.Context, cep string) (*WeatherResponse, error) {
	tracer := otel.Tracer("service-a")
	ctx, span := tracer.Start(ctx, "service-a: call-service-b")
	defer span.End()

	span.SetAttributes(attribute.String("cep", cep))

	log.Printf("Calling Service B with CEP: %s", cep)

	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.ServiceBURL+"?cep="+cep, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create request")
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := client.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to call service-b")
		log.Printf("Error calling service B: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))

	if resp.StatusCode == http.StatusNotFound {
		err := fmt.Errorf("cannot find zipcode")
		span.RecordError(err)
		span.SetStatus(codes.Error, "zipcode not found")
		return nil, err
	}

	if resp.StatusCode == http.StatusUnprocessableEntity {
		err := fmt.Errorf("invalid zipcode")
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid zipcode")
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("service-b returned status %d", resp.StatusCode)
		span.RecordError(err)
		span.SetStatus(codes.Error, "unexpected status from service-b")
		return nil, err
	}

	var weather WeatherResponse
	if err := json.NewDecoder(resp.Body).Decode(&weather); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to decode response")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return &weather, nil
}

func (h *Handler) validateCEP(ctx context.Context, r *http.Request) (*CEPRequest, error) {
	tracer := otel.Tracer("service-a")
	_, span := tracer.Start(ctx, "service-a: validate-cep")
	defer span.End()

	var req CEPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid request body")
		return nil, fmt.Errorf("invalid request")
	}

	if req.CEP == "" {
		err := fmt.Errorf("cep is required")
		span.RecordError(err)
		span.SetStatus(codes.Error, "cep is required")
		return nil, err
	}

	if !IsValidCEP(req.CEP) {
		err := fmt.Errorf("invalid zipcode")
		span.SetAttributes(attribute.String("cep", req.CEP))
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid zipcode format")
		return nil, err
	}

	span.SetAttributes(attribute.String("cep", req.CEP))
	span.SetStatus(codes.Ok, "")
	return &req, nil
}

func (h *Handler) HandleCEP(w http.ResponseWriter, r *http.Request) {
	tracer := otel.Tracer("service-a")
	ctx, span := tracer.Start(r.Context(), "service-a: handle-cep")
	defer span.End()

	req, err := h.validateCEP(ctx, r)
	if err != nil {
		span.RecordError(err)
		switch err.Error() {
		case "invalid request":
			span.SetStatus(codes.Error, "invalid request")
			WriteError(w, "invalid request", http.StatusBadRequest)
		case "cep is required":
			span.SetStatus(codes.Error, "cep is required")
			WriteError(w, "cep is required", http.StatusBadRequest)
		case "invalid zipcode":
			span.SetStatus(codes.Error, "invalid zipcode")
			WriteError(w, "invalid zipcode", http.StatusUnprocessableEntity)
		}
		return
	}

	span.SetAttributes(attribute.String("cep", req.CEP))
	log.Printf("Processing CEP: %s", req.CEP)

	weatherData, err := h.callServiceB(ctx, req.CEP)
	if err != nil {
		log.Printf("Error calling service B: %v", err)
		span.RecordError(err)
		switch err.Error() {
		case "cannot find zipcode":
			span.SetStatus(codes.Error, "zipcode not found")
			WriteError(w, "can not find zipcode", http.StatusNotFound)
		case "invalid zipcode":
			span.SetStatus(codes.Error, "invalid zipcode")
			WriteError(w, "invalid zipcode", http.StatusUnprocessableEntity)
		default:
			span.SetStatus(codes.Error, "failed to get weather data")
			WriteError(w, "failed to get weather data", http.StatusInternalServerError)
		}
		return
	}

	span.SetStatus(codes.Ok, "")
	WriteJSON(w, WeatherResponse{
		City:  weatherData.City,
		TempC: weatherData.TempC,
		TempF: weatherData.TempF,
		TempK: weatherData.TempK,
	}, http.StatusOK)
}

func SetupRouter(h *Handler) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Post("/service-a", h.HandleCEP)

	return otelhttp.NewHandler(r, "service-a-server")
}
