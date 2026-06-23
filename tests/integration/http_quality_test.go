package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestRequestIDMiddlewarePreservesIncomingHeader(t *testing.T) {
	client := newHTTPClient(t)
	request, err := http.NewRequest(http.MethodGet, apiBaseURL()+"/health", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("X-Request-ID", "study-request-id")

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	defer response.Body.Close()

	if response.Header.Get("X-Request-ID") != "study-request-id" {
		t.Fatalf("expected X-Request-ID to be preserved, got %q", response.Header.Get("X-Request-ID"))
	}
}

func TestErrorResponseIncludesRequestID(t *testing.T) {
	client := newHTTPClient(t)
	request, err := http.NewRequest(http.MethodGet, apiBaseURL()+"/v1/me/session", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("X-Request-ID", "error-request-id")

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, response.StatusCode)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	var payload struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response body %q: %v", string(body), err)
	}
	if payload.Error.Code != "SESSION_REQUIRED" {
		t.Fatalf("expected SESSION_REQUIRED error code, got %q", payload.Error.Code)
	}
	if payload.Error.RequestID != "error-request-id" {
		t.Fatalf("expected request_id in error response, got %q", payload.Error.RequestID)
	}
}
