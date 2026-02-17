package api

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
)

var cepRegex = regexp.MustCompile(`^\d{8}$`)

func WriteJSON(w http.ResponseWriter, data interface{}, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Error encoding JSON: %v", err)
	}
}

func WriteError(w http.ResponseWriter, msg string, code int) {
	WriteJSON(w, ErrorResponse{Message: msg}, code)
}

func IsValidCEP(cep string) bool {
	return cepRegex.MatchString(cep)
}
