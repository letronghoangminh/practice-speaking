package services

import (
	"context"
	"testing"
	"time"

	"practice-speaking/backend/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeAI struct {
	followUp bool
}

func (f fakeAI) GenerateBaseline(ctx context.Context, input BaselineInput) (BaselinePlan, error) {
	return BaselinePlan{Topics: []BaselineTopic{
		{
			Title:           "Kubernetes reliability",
			Category:        "kubernetes",
			QuestionType:    models.QuestionTypeScenario,
			InitialQuestion: "How would you debug restarting pods?",
		},
		{
			Title:           "Incident response",
			Category:        "incident-management",
			QuestionType:    models.QuestionTypeScenario,
			InitialQuestion: "How do you run an incident?",
		},
	}}, nil
}

func (f fakeAI) EvaluateAnswer(ctx context.Context, input TurnInput) (TurnEvaluation, error) {
	return TurnEvaluation{
		TranscriptSummary: "summary",
		TechnicalScore:    70,
		EnglishScore:      80,
		Strengths:         []string{"structured"},
		Improvements:      []string{"add metrics"},
		EnglishNotes:      []string{"speak shorter"},
		TechnicalNotes:    []string{"name signals"},
		ReferenceAnswer:   "reference answer",
		ShouldFollowUp:    f.followUp,
		FollowUpQuestion:  "Which metric proves recovery?",
		NextQuestionType:  models.QuestionTypeScenario,
	}, nil
}

func (f fakeAI) GenerateFinalReport(ctx context.Context, input FinalReportInput) (FinalReport, error) {
	technical, english, overall := AggregateScores(input.Turns)
	return FinalReport{
		Summary:        "done",
		TechnicalScore: technical,
		EnglishScore:   english,
		OverallScore:   overall,
	}, nil
}

func (f fakeAI) Transcribe(ctx context.Context, fileName string, contentType string, audio []byte) (string, error) {
	return "transcribed answer", nil
}

func (f fakeAI) Synthesize(ctx context.Context, text string) (AudioResult, error) {
	return AudioResult{}, nil
}

func TestMaxThreeQuestionsPerTopic(t *testing.T) {
	service := newTestService(t, fakeAI{followUp: true})
	ctx := context.Background()
	envelope, err := service.CreateSession(ctx, CreateSessionInput{Mode: models.SessionModePractice})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	firstTopicID := envelope.Session.Topics[0].ID

	for i := 0; i < 3; i++ {
		envelope, err = service.SubmitTextAnswer(ctx, envelope.Session.ID, "I would check metrics logs events rollback and validate service recovery.")
		if err != nil {
			t.Fatalf("submit answer %d: %v", i+1, err)
		}
	}

	if envelope.Session.Status != models.SessionStatusActive {
		t.Fatalf("expected session to remain active on second topic, got %s", envelope.Session.Status)
	}
	if envelope.CurrentQuestion == nil {
		t.Fatal("expected next topic question")
	}
	if envelope.CurrentQuestion.TopicID == firstTopicID {
		t.Fatal("expected engine to move away from first topic after three questions")
	}
}

func TestDeadlineFinalizesSession(t *testing.T) {
	service := newTestService(t, fakeAI{followUp: true})
	ctx := context.Background()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	envelope, err := service.CreateSession(ctx, CreateSessionInput{Mode: models.SessionModePractice})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	service.now = func() time.Time { return now.Add(practiceDuration + time.Second) }
	envelope, err = service.SubmitTextAnswer(ctx, envelope.Session.ID, "late answer")
	if err != nil {
		t.Fatalf("submit after deadline: %v", err)
	}
	if envelope.Session.Status != models.SessionStatusCompleted {
		t.Fatalf("expected completed session, got %s", envelope.Session.Status)
	}
}

func TestSessionDurations(t *testing.T) {
	service := newTestService(t, fakeAI{})
	ctx := context.Background()
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	practice, err := service.CreateSession(ctx, CreateSessionInput{Mode: models.SessionModePractice})
	if err != nil {
		t.Fatalf("create practice session: %v", err)
	}
	if got := practice.Session.DeadlineAt.Sub(practice.Session.StartedAt); got != 10*time.Minute {
		t.Fatalf("practice duration = %s, want 10m", got)
	}

	interview, err := service.CreateSession(ctx, CreateSessionInput{
		Mode:   models.SessionModeInterview,
		JDText: "DevOps role with Kubernetes and Terraform.",
		CVText: "Candidate with SRE experience.",
	})
	if err != nil {
		t.Fatalf("create interview session: %v", err)
	}
	if got := interview.Session.DeadlineAt.Sub(interview.Session.StartedAt); got != 20*time.Minute {
		t.Fatalf("interview duration = %s, want 20m", got)
	}
}

func TestSkipCurrentQuestionMovesTopicAndDoesNotScore(t *testing.T) {
	service := newTestService(t, fakeAI{})
	ctx := context.Background()
	envelope, err := service.CreateSession(ctx, CreateSessionInput{Mode: models.SessionModePractice})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	firstTopicID := envelope.Session.Topics[0].ID

	envelope, err = service.SkipCurrentQuestion(ctx, envelope.Session.ID)
	if err != nil {
		t.Fatalf("skip question: %v", err)
	}
	if envelope.CurrentQuestion == nil {
		t.Fatal("expected next question after skipping")
	}
	if envelope.CurrentQuestion.TopicID == firstTopicID {
		t.Fatal("expected skip to move to the next topic")
	}
	if envelope.Session.Topics[0].AskedCount != 0 {
		t.Fatalf("skipped topic asked count = %d, want 0", envelope.Session.Topics[0].AskedCount)
	}
	if !envelope.Session.Topics[0].Completed {
		t.Fatal("expected skipped topic to be completed")
	}
	if envelope.Session.Turns[0].SkippedAt == nil {
		t.Fatal("expected first turn to be marked skipped")
	}
	if envelope.Session.Turns[0].AnsweredAt != nil {
		t.Fatal("skipped turn must not be answered")
	}
	if technical, english, overall := AggregateScores(envelope.Session.Turns); technical != 0 || english != 0 || overall != 0 {
		t.Fatalf("skipped turn affected scores: technical=%v english=%v overall=%v", technical, english, overall)
	}

	envelope, err = service.SubmitTextAnswer(ctx, envelope.Session.ID, "I would define roles, communicate clearly, inspect metrics and logs, and validate recovery.")
	if err != nil {
		t.Fatalf("submit answer after skip: %v", err)
	}
	if len(answeredTurns(envelope.Session.Turns)) != 1 {
		t.Fatalf("expected only one scored answer, got %d", len(answeredTurns(envelope.Session.Turns)))
	}
	if envelope.Session.TechnicalScore != 70 || envelope.Session.EnglishScore != 80 || envelope.Session.OverallScore != 74 {
		t.Fatalf("unexpected final scores after skip: technical=%v english=%v overall=%v", envelope.Session.TechnicalScore, envelope.Session.EnglishScore, envelope.Session.OverallScore)
	}
}

func TestAggregateScores(t *testing.T) {
	now := time.Now()
	skipped := time.Now()
	technical, english, overall := AggregateScores([]models.Turn{
		{TechnicalScore: 80, EnglishScore: 70, AnsweredAt: &now},
		{TechnicalScore: 60, EnglishScore: 90, AnsweredAt: &now},
		{TechnicalScore: 100, EnglishScore: 100},
		{TechnicalScore: 100, EnglishScore: 100, SkippedAt: &skipped},
	})

	if technical != 70 || english != 80 || overall != 74 {
		t.Fatalf("unexpected scores: technical=%v english=%v overall=%v", technical, english, overall)
	}
}

func TestDocumentValidation(t *testing.T) {
	if _, err := ExtractDocumentText("notes.exe", []byte("hello")); err == nil {
		t.Fatal("expected unsupported file type error")
	}
	text, err := ExtractDocumentText("notes.md", []byte("# hello"))
	if err != nil {
		t.Fatalf("expected markdown text: %v", err)
	}
	if text != "# hello" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestParseJSONPayloadFromFence(t *testing.T) {
	var parsed struct {
		Name string `json:"name"`
	}
	if err := ParseJSONPayload("```json\n{\"name\":\"sre\"}\n```", &parsed); err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if parsed.Name != "sre" {
		t.Fatalf("unexpected name: %s", parsed.Name)
	}
}

func newTestService(t *testing.T, ai AIClient) *SessionService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Session{}, &models.Topic{}, &models.Turn{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	return NewSessionService(db, nil, ai)
}
