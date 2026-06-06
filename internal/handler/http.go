package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bvgroup-co/postal-sendgrid/internal/sendgrid"
)

type APIError struct {
	Status  int
	Message string
	Field   string
}

func (e APIError) Error() string {
	return e.Message
}

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

func WriteError(w http.ResponseWriter, err error) {
	var apiErr APIError
	if errors.As(err, &apiErr) {
		WriteJSON(w, apiErr.Status, sendgrid.ErrorResponse{Errors: []sendgrid.ErrorItem{{Message: apiErr.Message, Field: apiErr.Field}}})
		return
	}
	WriteJSON(w, http.StatusInternalServerError, sendgrid.ErrorResponse{Errors: []sendgrid.ErrorItem{{Message: "Internal server error"}}})
}

func DecodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return APIError{Status: http.StatusBadRequest, Message: "Request body must be valid JSON"}
	}
	return nil
}
