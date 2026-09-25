package httpapi

import (
	"encoding/json"
	"net/http"
)

type requestIDKey struct{}

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	requestID, _ := r.Context().Value(requestIDKey{}).(string)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: ErrorBody{
		Code: code, Message: message, RequestID: requestID,
	}})
}
