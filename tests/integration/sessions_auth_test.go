package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"testing"
	"time"
)

func apiBaseURL() string {
	value := os.Getenv("API_BASE_URL")
	if value == "" {
		return "http://localhost:8080"
	}
	return value
}

func newHTTPClient(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}

	return &http.Client{
		Jar:     jar,
		Timeout: 5 * time.Second,
	}
}

func postJSON(t *testing.T, client *http.Client, path string, payload any) *http.Response {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, apiBaseURL()+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	return response
}

func getJSON(t *testing.T, client *http.Client, path string) *http.Response {
	t.Helper()

	request, err := http.NewRequest(http.MethodGet, apiBaseURL()+path, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	return response
}

func decodeBody[T any](t *testing.T, response *http.Response) T {
	t.Helper()
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	var payload T
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response body %q: %v", string(body), err)
	}
	return payload
}

func uniqueEmail() string {
	return fmt.Sprintf("user-%d@example.com", time.Now().UnixNano())
}

func createAnonymousSessionForTest(t *testing.T, client *http.Client) string {
	t.Helper()

	response := postJSON(t, client, "/v1/sessions/anonymous", map[string]string{})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, response.StatusCode)
	}

	payload := decodeBody[struct {
		VisitorSessionID string `json:"visitor_session_id"`
	}](t, response)
	if payload.VisitorSessionID == "" {
		t.Fatal("expected visitor_session_id")
	}
	return payload.VisitorSessionID
}

func registerUserForTest(t *testing.T, client *http.Client, email string, password string) string {
	t.Helper()

	response := postJSON(t, client, "/v1/auth/register", map[string]string{
		"email":    email,
		"password": password,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, response.StatusCode)
	}

	payload := decodeBody[struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}](t, response)
	if payload.ID == "" {
		t.Fatal("expected user id")
	}
	if payload.Email != email {
		t.Fatalf("expected email %q, got %q", email, payload.Email)
	}
	return payload.ID
}

func TestCreateAnonymousSession(t *testing.T) {
	client := newHTTPClient(t)

	response := postJSON(t, client, "/v1/sessions/anonymous", map[string]string{})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, response.StatusCode)
	}

	payload := decodeBody[struct {
		VisitorSessionID string `json:"visitor_session_id"`
	}](t, response)
	if payload.VisitorSessionID == "" {
		t.Fatal("expected visitor_session_id")
	}

	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "visitor_session" {
		t.Fatalf("expected visitor_session cookie, got %q", cookie.Name)
	}
	if !cookie.HttpOnly {
		t.Fatal("expected HTTP-only cookie")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected SameSite=Lax, got %v", cookie.SameSite)
	}
	if cookie.Secure {
		t.Fatal("expected Secure=false in local environment")
	}
	if cookie.Value == "" {
		t.Fatal("expected non-empty cookie value")
	}
}

func TestReadCurrentSessionByCookie(t *testing.T) {
	client := newHTTPClient(t)
	sessionID := createAnonymousSessionForTest(t, client)

	response := getJSON(t, client, "/v1/me/session")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.StatusCode)
	}

	payload := decodeBody[struct {
		ID     string  `json:"id"`
		UserID *string `json:"user_id"`
	}](t, response)
	if payload.ID != sessionID {
		t.Fatalf("expected session id %q, got %q", sessionID, payload.ID)
	}
	if payload.UserID != nil {
		t.Fatalf("expected anonymous session user_id=nil, got %q", *payload.UserID)
	}
}

func TestRegisterUser(t *testing.T) {
	client := newHTTPClient(t)
	email := uniqueEmail()

	userID := registerUserForTest(t, client, email, "very-secure-password")
	if userID == "" {
		t.Fatal("expected user id")
	}
}

func TestLoginLinksAnonymousSession(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)

	email := uniqueEmail()
	password := "very-secure-password"
	userID := registerUserForTest(t, client, email, password)

	loginResponse := postJSON(t, client, "/v1/auth/login", map[string]string{
		"email":    email,
		"password": password,
	})
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, loginResponse.StatusCode)
	}
	loginPayload := decodeBody[struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}](t, loginResponse)
	if loginPayload.ID != userID {
		t.Fatalf("expected logged-in user id %q, got %q", userID, loginPayload.ID)
	}
	if loginPayload.Email != email {
		t.Fatalf("expected email %q, got %q", email, loginPayload.Email)
	}

	sessionResponse := getJSON(t, client, "/v1/me/session")
	if sessionResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, sessionResponse.StatusCode)
	}
	sessionPayload := decodeBody[struct {
		UserID *string `json:"user_id"`
	}](t, sessionResponse)
	if sessionPayload.UserID == nil {
		t.Fatal("expected linked user_id")
	}
	if *sessionPayload.UserID != userID {
		t.Fatalf("expected linked user_id %q, got %q", userID, *sessionPayload.UserID)
	}
}

func TestLogoutRemovesSessionCookie(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)

	logoutResponse := postJSON(t, client, "/v1/auth/logout", map[string]string{})
	if logoutResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, logoutResponse.StatusCode)
	}
	defer logoutResponse.Body.Close()

	cookies := logoutResponse.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one clearing cookie, got %d", len(cookies))
	}
	if cookies[0].Name != "visitor_session" {
		t.Fatalf("expected visitor_session cookie, got %q", cookies[0].Name)
	}
	if cookies[0].MaxAge >= 0 {
		t.Fatalf("expected clearing cookie MaxAge < 0, got %d", cookies[0].MaxAge)
	}

	sessionResponse := getJSON(t, client, "/v1/me/session")
	defer sessionResponse.Body.Close()
	if sessionResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected status %d after logout, got %d", http.StatusUnauthorized, sessionResponse.StatusCode)
	}
}
