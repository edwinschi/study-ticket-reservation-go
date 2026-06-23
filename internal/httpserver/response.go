package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func WriteJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// At this point headers are already written. The best practical action is to log.
		slog.Default().Error("failed to encode response", "error", err)
	}
}

func WriteError(w http.ResponseWriter, statusCode int, code string, message string) {
	WriteJSON(w, statusCode, ErrorBody{
		Error: ErrorDetail{
			Code:      code,
			Message:   message,
			RequestID: requestIDFromWriter(w),
		},
	})
}

func WriteAppError(w http.ResponseWriter, err AppError) {
	WriteError(w, err.StatusCode, err.Code, err.Message)
}

func requestIDFromWriter(w http.ResponseWriter) string {
	return w.Header().Get(RequestIDHeader)
}
