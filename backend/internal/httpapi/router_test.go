package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"practice-speaking/backend/internal/config"
	"practice-speaking/backend/internal/models"
	"practice-speaking/backend/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type apiFakeAI struct{}

func (apiFakeAI) GenerateBaseline(ctx context.Context, input services.BaselineInput) (services.BaselinePlan, error) {
	return services.BaselinePlan{Topics: []services.BaselineTopic{
		{Title: "SLOs", Category: "reliability", QuestionType: models.QuestionTypeTheory, InitialQuestion: "How would you define an SLO?"},
		{Title: "Terraform", Category: "terraform", QuestionType: models.QuestionTypePractice, InitialQuestion: "How would you handle Terraform drift?"},
	}}, nil
}

func (apiFakeAI) EvaluateAnswer(ctx context.Context, input services.TurnInput) (services.TurnEvaluation, error) {
	return services.TurnEvaluation{
		TranscriptSummary: "summary",
		TechnicalScore:    80,
		EnglishScore:      80,
		ReferenceAnswer:   "Use user-impacting SLIs and clear objectives.",
		NextQuestionType:  models.QuestionTypeScenario,
	}, nil
}

func (apiFakeAI) GenerateFinalReport(ctx context.Context, input services.FinalReportInput) (services.FinalReport, error) {
	return services.FinalReport{Summary: "ok", TechnicalScore: 80, EnglishScore: 80, OverallScore: 80}, nil
}

func (apiFakeAI) Transcribe(ctx context.Context, fileName string, contentType string, audio []byte) (string, error) {
	return "answer", nil
}

func (apiFakeAI) Synthesize(ctx context.Context, text string) (services.AudioResult, error) {
	return services.AudioResult{}, nil
}

func TestCreatePracticeSessionAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader("mode=practice"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "How would you define an SLO?") {
		t.Fatalf("response did not include first question: %s", rec.Body.String())
	}
}

func TestSkipQuestionAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newTestHandler(t)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader("mode=practice"))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}

	var created services.SessionEnvelope
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	firstTopicID := created.CurrentQuestion.TopicID

	skipReq := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+strconv.Itoa(int(created.Session.ID))+"/skip-question", nil)
	skipRec := httptest.NewRecorder()
	handler.ServeHTTP(skipRec, skipReq)
	if skipRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", skipRec.Code, skipRec.Body.String())
	}

	var skipped services.SessionEnvelope
	if err := json.Unmarshal(skipRec.Body.Bytes(), &skipped); err != nil {
		t.Fatalf("decode skip response: %v", err)
	}
	if skipped.CurrentQuestion == nil {
		t.Fatal("expected next question after skip")
	}
	if skipped.CurrentQuestion.TopicID == firstTopicID {
		t.Fatal("expected skip to advance to another topic")
	}
	if skipped.Session.Topics[0].AskedCount != 0 || !skipped.Session.Topics[0].Completed {
		t.Fatalf("skipped topic was counted incorrectly: asked=%d completed=%v", skipped.Session.Topics[0].AskedCount, skipped.Session.Topics[0].Completed)
	}
	if skipped.Session.Turns[0].SkippedAt == nil || skipped.Session.Turns[0].AnsweredAt != nil {
		t.Fatalf("skipped turn state is invalid: skipped_at=%v answered_at=%v", skipped.Session.Turns[0].SkippedAt, skipped.Session.Turns[0].AnsweredAt)
	}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Session{}, &models.Topic{}, &models.Turn{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	service := services.NewSessionService(db, nil, apiFakeAI{})
	return New(service, config.Config{AllowedOrigins: []string{"http://localhost:3000"}})
}
