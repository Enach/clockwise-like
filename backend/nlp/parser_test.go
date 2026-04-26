package nlp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Enach/paceday/backend/storage"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestStartOfWeekLocal(t *testing.T) {
	// Wednesday
	wed := time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC)
	monday := startOfWeekLocal(wed)
	if monday.Weekday() != time.Monday {
		t.Errorf("expected Monday, got %v", monday.Weekday())
	}
	if monday.Hour() != 0 {
		t.Error("expected midnight")
	}

	// Sunday (special case)
	sun := time.Date(2025, 1, 12, 0, 0, 0, 0, time.UTC)
	monday2 := startOfWeekLocal(sun)
	if monday2.Weekday() != time.Monday {
		t.Errorf("startOfWeekLocal(Sunday) = %v, want Monday", monday2.Weekday())
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	s := &storage.Settings{
		Timezone:  "UTC",
		WorkStart: "09:00",
		WorkEnd:   "18:00",
	}
	prompt, err := buildSystemPrompt(s)
	if err != nil {
		t.Fatalf("buildSystemPrompt: %v", err)
	}
	if !strings.Contains(prompt, "schedule_meeting") {
		t.Error("prompt should mention schedule_meeting")
	}
	if !strings.Contains(prompt, "UTC") {
		t.Error("prompt should contain timezone")
	}
	if !strings.Contains(prompt, "09:00") {
		t.Error("prompt should contain work start")
	}
}

func TestBuildLLMClient_NoKey(t *testing.T) {
	db := openTestDB(t)
	svc := &NLPService{DB: db}

	s := &storage.Settings{LLMProvider: "openai", LLMAPIKey: ""}
	_, err := svc.buildLLMClient(s)
	if err == nil {
		t.Error("expected error with no API key for openai")
	}
}

func TestBuildLLMClient_Ollama(t *testing.T) {
	db := openTestDB(t)
	svc := &NLPService{DB: db}

	s := &storage.Settings{LLMProvider: "ollama", LLMAPIKey: "", LLMBaseURL: "http://localhost:11434"}
	client, err := svc.buildLLMClient(s)
	if err != nil {
		t.Fatalf("expected no error for ollama: %v", err)
	}
	if client == nil {
		t.Error("expected non-nil client")
	}
}

func TestBuildLLMClient_OpenAI(t *testing.T) {
	db := openTestDB(t)
	svc := &NLPService{DB: db}

	s := &storage.Settings{LLMProvider: "openai", LLMAPIKey: "sk-test", LLMModel: "gpt-4o-mini"}
	client, err := svc.buildLLMClient(s)
	if err != nil {
		t.Fatalf("expected no error for openai with key: %v", err)
	}
	if client == nil {
		t.Error("expected non-nil client")
	}
}

func TestBuildLLMClient_Anthropic(t *testing.T) {
	db := openTestDB(t)
	svc := &NLPService{DB: db}

	s := &storage.Settings{LLMProvider: "anthropic", LLMAPIKey: "ant-test"}
	client, err := svc.buildLLMClient(s)
	if err != nil {
		t.Fatalf("expected no error: %v", err)
	}
	if client == nil {
		t.Error("expected non-nil client")
	}
}

func TestBuildLLMClient_DefaultProvider(t *testing.T) {
	db := openTestDB(t)
	svc := &NLPService{DB: db}

	s := &storage.Settings{LLMProvider: "", LLMAPIKey: ""}
	_, err := svc.buildLLMClient(s)
	if err == nil {
		t.Error("expected error for empty provider")
	}
}

func TestBuildLLMClient_DefaultModels(t *testing.T) {
	db := openTestDB(t)
	svc := &NLPService{DB: db}

	// Test default model for ollama (empty model → llama3.2)
	s := &storage.Settings{LLMProvider: "ollama", LLMModel: ""}
	client, err := svc.buildLLMClient(s)
	if err != nil {
		t.Fatalf("ollama no model: %v", err)
	}
	ollamaClient, ok := client.(*OllamaClient)
	if !ok {
		t.Fatal("expected *OllamaClient")
	}
	if ollamaClient.Model != "llama3.2" {
		t.Errorf("default ollama model = %q, want llama3.2", ollamaClient.Model)
	}
}

func TestOpenAIClient_Complete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": `{"intent":"unknown","error":"test"}`}},
			},
		})
	}))
	defer srv.Close()

	client := &OpenAIClient{APIKey: "sk-test", Model: "gpt-4o-mini", BaseURL: srv.URL}
	result, err := client.Complete(context.Background(), "system", "user message")
	if err != nil {
		t.Fatalf("OpenAIClient.Complete: %v", err)
	}
	if !strings.Contains(result, "unknown") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestAnthropicClient_Complete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"content": []map[string]string{
				{"text": `{"intent":"schedule_focus","duration_minutes":60,"range_start":"2025-01-06","range_end":"2025-01-06"}`},
			},
		})
	}))
	defer srv.Close()

	client := &AnthropicClient{APIKey: "ant-test", Model: "claude-haiku-4-5-20251001", BaseURL: srv.URL}
	result, err := client.Complete(context.Background(), "system", "user message")
	if err != nil {
		t.Fatalf("AnthropicClient.Complete: %v", err)
	}
	if !strings.Contains(result, "schedule_focus") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestPostJSON_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json content type")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"result":"ok"}`)
	}))
	defer srv.Close()

	result, err := postJSON(context.Background(), srv.URL, map[string]string{"X-Test": "1"},
		map[string]string{"key": "value"},
		func(body []byte) (string, error) {
			return string(body), nil
		})
	if err != nil {
		t.Fatalf("postJSON: %v", err)
	}
	if !strings.Contains(result, "ok") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestPostJSON_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "server error")
	}))
	defer srv.Close()

	_, err := postJSON(context.Background(), srv.URL, nil, map[string]string{},
		func(body []byte) (string, error) { return string(body), nil })
	if err == nil {
		t.Error("expected error on HTTP 500")
	}
}

func TestOllamaClient_Complete_Mock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": map[string]string{
				"content": `{"intent":"schedule_focus","duration_minutes":60,"range_start":"2025-01-06","range_end":"2025-01-06"}`,
			},
		})
	}))
	defer srv.Close()

	client := &OllamaClient{BaseURL: srv.URL, Model: "llama3.2"}
	result, err := client.Complete(context.Background(), "system prompt", "block focus time tomorrow")
	if err != nil {
		t.Fatalf("OllamaClient.Complete: %v", err)
	}
	if !strings.Contains(result, "schedule_focus") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestNLPService_Parse_NoLLM(t *testing.T) {
	db := openTestDB(t)
	// DB default for llm_provider is "ollama" (would try localhost:11434); force empty.
	storage.SaveSettings(db, &storage.Settings{LLMProvider: ""})
	svc := &NLPService{DB: db}
	result, err := svc.Parse(context.Background(), "schedule a meeting tomorrow")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if result.Intent != "unknown" {
		t.Errorf("expected unknown intent, got %q", result.Intent)
	}
	if !strings.Contains(result.Error, "LLM not configured") {
		t.Errorf("error should mention LLM not configured, got: %q", result.Error)
	}
}

func TestNLPService_Parse_WithOllama(t *testing.T) {
	focusResponse := `{"intent":"schedule_focus","duration_minutes":90,"range_start":"2025-01-07","range_end":"2025-01-07"}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": map[string]string{"content": focusResponse},
		})
	}))
	defer srv.Close()

	db := openTestDB(t)
	s := &storage.Settings{
		LLMProvider: "ollama",
		LLMModel:    "llama3.2",
		LLMBaseURL:  srv.URL,
		Timezone:    "UTC",
		WorkStart:   "09:00",
		WorkEnd:     "18:00",
	}
	storage.SaveSettings(db, s)

	svc := &NLPService{DB: db}
	result, err := svc.Parse(context.Background(), "block focus time tomorrow")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if result.Intent != "schedule_focus" {
		t.Errorf("expected schedule_focus, got %q", result.Intent)
	}
	if result.DurationMinutes != 90 {
		t.Errorf("expected 90 min, got %d", result.DurationMinutes)
	}
}

func TestNLPService_Parse_ScheduleMeeting(t *testing.T) {
	meetingResponse := `{"intent":"schedule_meeting","title":"Team Sync","duration_minutes":30,"attendees":["alice@co.com"],"range_start":"2025-01-06","range_end":"2025-01-10"}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": map[string]string{"content": meetingResponse},
		})
	}))
	defer srv.Close()

	db := openTestDB(t)
	s := &storage.Settings{
		LLMProvider: "ollama",
		LLMBaseURL:  srv.URL,
		Timezone:    "UTC",
		WorkStart:   "09:00",
		WorkEnd:     "18:00",
	}
	storage.SaveSettings(db, s)

	svc := &NLPService{DB: db}
	result, err := svc.Parse(context.Background(), "schedule 30 min meeting with alice@co.com this week")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if result.Intent != "schedule_meeting" {
		t.Errorf("expected schedule_meeting, got %q", result.Intent)
	}
	if result.Title != "Team Sync" {
		t.Errorf("Title = %q, want Team Sync", result.Title)
	}
}

func TestNLPService_Parse_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": map[string]string{"content": "not valid json at all!!!"},
		})
	}))
	defer srv.Close()

	db := openTestDB(t)
	s := &storage.Settings{
		LLMProvider: "ollama",
		LLMBaseURL:  srv.URL,
		Timezone:    "UTC",
	}
	storage.SaveSettings(db, s)

	svc := &NLPService{DB: db}
	result, err := svc.Parse(context.Background(), "something unclear")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if result.Intent != "unknown" {
		t.Errorf("expected unknown for invalid json, got %q", result.Intent)
	}
}
