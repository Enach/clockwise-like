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

	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/internal/testdb"
	"github.com/Enach/paceday/backend/storage"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn, cleanup, err := testdb.Create()
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	db, err := storage.Open(dsn)
	if err != nil {
		_ = cleanup()
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		if err := cleanup(); err != nil {
			t.Errorf("cleanup test database: %v", err)
		}
	})
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

// The TestBuildLLMClient_* tests are replaced by TestDecideSaved_* in
// destination_internal_test.go (NEW-llm-bring-your-own: one decision table;
// TestBuildLLMClient_Ollama and the Ollama default model are gone with
// Ollama, replaced by the provider_removed row).

// openAIParseServer answers every OpenAI chat request with content, through
// the public-host Given (PAC-66-B §B6), and returns the user's settings for
// it: OpenAI with a key bound.
func openAIParseServer(t *testing.T, content string) *storage.Settings {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]string{"content": content}}},
		})
	}))
	t.Cleanup(srv.Close)
	forwardUserRequestsTo(t, srv)
	s := &storage.Settings{LLMProvider: "openai", Timezone: "UTC", WorkStart: "09:00", WorkEnd: "18:00"}
	s.LLMAPIKeys.Set("openai", "sk-test")
	return s
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

	forwardUserRequestsTo(t, srv) // PAC-66-B §B6 public-host Given
	client := &OpenAIClient{APIKey: "sk-test", Model: "gpt-4o-mini"}
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

	forwardUserRequestsTo(t, srv) // PAC-66-B §B6 public-host Given
	client := &AnthropicClient{APIKey: "ant-test", Model: "claude-haiku-4-5-20251001"}
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

// TestOllamaClient_Complete_Mock is gone with the Ollama client
// (NEW-llm-bring-your-own O-4); the Azure, Bedrock and Vertex clients are
// pinned by api/byok_test.go (AC-3, AC-4, AC-5) and destination_test.go.

func TestNLPService_Parse_NoLLM(t *testing.T) { // NEW-llm-bring-your-own AC-11, AC-13, AC-35
	db := openTestDB(t)
	ctx := settingsUser(t, db, &storage.Settings{LLMProvider: ""})
	svc := &NLPService{DB: db}
	result, err := svc.Parse(ctx, "schedule a meeting tomorrow")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if result.Intent != "unknown" || result.NotConfigured == nil || result.NotConfigured.Reason != ReasonOff {
		t.Errorf("result = %+v, want intent unknown with not_configured off", result)
	}
	if !strings.Contains(result.Error, "AI features are off") || result.Error != result.NotConfigured.Message {
		t.Errorf("error = %q, want the not_configured sentence", result.Error)
	}
	body, _ := json.Marshal(result)
	if strings.Contains(string(body), "range_start") || strings.Contains(string(body), "0001-01-01") {
		t.Errorf("a result with no range carries one (API-080): %s", body)
	}
}

func TestNLPService_Parse_FocusRequest(t *testing.T) { // was TestNLPService_Parse_WithOllama
	db := openTestDB(t)
	ctx := settingsUser(t, db, openAIParseServer(t, `{"intent":"schedule_focus","duration_minutes":90,"range_start":"2025-01-07","range_end":"2025-01-07"}`))

	svc := &NLPService{DB: db}
	result, err := svc.Parse(ctx, "block focus time tomorrow")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if result.Intent != "schedule_focus" {
		t.Errorf("expected schedule_focus, got %q", result.Intent)
	}
	if result.DurationMinutes != 90 {
		t.Errorf("expected 90 min, got %d", result.DurationMinutes)
	}
	body, _ := json.Marshal(result)
	if !strings.Contains(string(body), `"range_start":"2025-01-07T00:00:00Z"`) || result.NotConfigured != nil {
		t.Errorf("a found range must be present and not_configured absent (AC-35): %s", body)
	}
}

func TestNLPService_Parse_ScheduleMeeting(t *testing.T) {
	meetingResponse := `{"intent":"schedule_meeting","title":"Team Sync","duration_minutes":30,"attendees":["alice@co.com"],"range_start":"2025-01-06","range_end":"2025-01-10"}`
	db := openTestDB(t)
	ctx := settingsUser(t, db, openAIParseServer(t, meetingResponse))

	svc := &NLPService{DB: db}
	result, err := svc.Parse(ctx, "schedule 30 min meeting with alice@co.com this week")
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
	db := openTestDB(t)
	ctx := settingsUser(t, db, openAIParseServer(t, "not valid json at all!!!"))

	svc := &NLPService{DB: db}
	result, err := svc.Parse(ctx, "something unclear")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if result.Intent != "unknown" {
		t.Errorf("expected unknown for invalid json, got %q", result.Intent)
	}
}

// settingsUser saves s as a new user's settings and returns a context carrying
// that user: NLPService.Parse reads the caller's own settings (PAC-24).
func settingsUser(t *testing.T, db *sql.DB, s *storage.Settings) context.Context {
	t.Helper()
	email := fmt.Sprintf("nlp-%d@example.com", time.Now().UnixNano())
	u, err := storage.UpsertUser(db, email, email, "", "google", email)
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	if err := storage.SaveSettingsForUser(db, u.ID, s); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	return context.WithValue(context.Background(), auth.UserIDKey, u.ID)
}

// PAC-23 AC-5 / AC-14: a parsed request is recorded against the caller in ctx,
// and the typed prompt reaches nobody else's log.
func TestParse_AuditEntrySubjectIsCallerFromContext(t *testing.T) {
	db := openTestDB(t)
	ctx := settingsUser(t, db, openAIParseServer(t, `{"intent":"schedule_focus","duration_minutes":60}`))
	otherCtx := settingsUser(t, db, &storage.Settings{LLMProvider: ""})
	caller, other := auth.UserIDFromContext(ctx), auth.UserIDFromContext(otherCtx)

	if _, err := (&NLPService{DB: db}).Parse(ctx, "block an hour of focus (audit prompt)"); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	mine, err := storage.ListAuditLog(db, caller, storage.AuditMaxLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0].Action != "nlp_parsed" || !strings.Contains(mine[0].Details, "audit prompt") {
		t.Errorf("caller's log = %+v, want one nlp_parsed carrying the prompt", mine)
	}
	if theirs, _ := storage.ListAuditLog(db, other, storage.AuditMaxLimit); len(theirs) != 0 {
		t.Errorf("other user's log = %+v, want empty", theirs)
	}
}
