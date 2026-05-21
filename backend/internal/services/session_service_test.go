package services

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"practice-speaking/backend/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeAI struct {
	followUp      bool
	imageText     string
	imageFileName string
	imageMIME     string
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

func (f fakeAI) ExtractImageText(ctx context.Context, fileName string, contentType string, image []byte) (string, error) {
	if f.imageText != "" {
		return f.imageText, nil
	}
	return "Extracted JD image text with Kubernetes and Terraform requirements.", nil
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

	customPractice, err := service.CreateSession(ctx, CreateSessionInput{Mode: models.SessionModePractice, DurationMinutes: 12})
	if err != nil {
		t.Fatalf("create custom practice session: %v", err)
	}
	if got := customPractice.Session.DeadlineAt.Sub(customPractice.Session.StartedAt); got != 12*time.Minute {
		t.Fatalf("custom practice duration = %s, want 12m", got)
	}

	customInterview, err := service.CreateSession(ctx, CreateSessionInput{
		Mode:            models.SessionModeInterview,
		DurationMinutes: 35,
		JDText:          "DevOps role with Kubernetes and Terraform.",
		CVText:          "Candidate with SRE experience.",
	})
	if err != nil {
		t.Fatalf("create custom interview session: %v", err)
	}
	if got := customInterview.Session.DeadlineAt.Sub(customInterview.Session.StartedAt); got != 35*time.Minute {
		t.Fatalf("custom interview duration = %s, want 35m", got)
	}
}

func TestSessionDurationValidation(t *testing.T) {
	service := newTestService(t, fakeAI{})
	ctx := context.Background()

	if _, err := service.CreateSession(ctx, CreateSessionInput{Mode: models.SessionModePractice, DurationMinutes: -1}); err == nil || !strings.Contains(err.Error(), "duration_minutes") {
		t.Fatalf("expected invalid duration error, got %v", err)
	}
	if _, err := service.CreateSession(ctx, CreateSessionInput{Mode: models.SessionModePractice, DurationMinutes: 121}); err == nil || !strings.Contains(err.Error(), "duration_minutes") {
		t.Fatalf("expected invalid duration error, got %v", err)
	}
}

func TestInterviewAddsCVExperienceTopics(t *testing.T) {
	service := newTestService(t, fakeAI{})
	ctx := context.Background()
	envelope, err := service.CreateSession(ctx, CreateSessionInput{
		Mode:   models.SessionModeInterview,
		JDText: "DevOps role with Kubernetes and Terraform.",
		CVText: "WORK EXPERIENCE\n• Designed Kubernetes platform work for CI/CD migration and incident response automation, reducing release risk by 40%.\n• Built Terraform modules for production clusters and improved deployment consistency.",
	})
	if err != nil {
		t.Fatalf("create interview session: %v", err)
	}

	experienceCount := 0
	for _, topic := range envelope.Session.Topics {
		if topic.Category == "cv-experience" {
			experienceCount++
		}
	}
	if experienceCount < 2 {
		t.Fatalf("expected at least 2 CV experience topics, got %d: %#v", experienceCount, envelope.Session.Topics)
	}
	if envelope.CurrentQuestion == nil {
		t.Fatal("expected current question")
	}
	if envelope.Session.Topics[0].Category != "cv-experience" {
		t.Fatalf("first topic category = %q, want cv-experience", envelope.Session.Topics[0].Category)
	}
	if !strings.Contains(envelope.CurrentQuestion.QuestionText, "Based on your CV") {
		t.Fatalf("first question should clearly reference the CV, got %q", envelope.CurrentQuestion.QuestionText)
	}
	if !strings.Contains(envelope.CurrentQuestion.QuestionText, "Kubernetes platform work") {
		t.Fatalf("first question should include a CV claim, got %q", envelope.CurrentQuestion.QuestionText)
	}
}

func TestExtractCVExperienceClaimsIgnoresPDFHeaderAndSummaryNoise(t *testing.T) {
	cvText := `Le Trong Hoang MinhDevOps Engineer+84943707317✉candidate@example.comhttps://hackmd.io/@profileSUMMARYSite Reliability Engineer / DevOps Engineer with 5+ years of experience designing secure, scalable cloud-native platforms on AWS and GCP.WORK EXPERIENCEShopeeJul 2025-PresentSite Reliability Engineer | Ho Chi Minh City, VietnamMain responsibilities:•Designed and implemented a SecOps platform for Vietnam product infrastructure: enforcing runtime security across Kubernetes clusters and WAF layers, blocking 300+ malicious attacks per month.•Designed and built SRE agent and AIOps/ChatOps integration with platform engineering, reducing SRE MTTR by ~70%.Technologies used: GCP, Kubernetes, Terraform, Gitlab CI, ArgoCD, AIOps, Victoria Metrics, Grafana.EDUCATIONDa Nang University`

	claims := extractCVExperienceClaims(cvText, 2)
	if len(claims) != 2 {
		t.Fatalf("claims = %#v, want 2", claims)
	}
	joined := strings.Join(claims, "\n")
	for _, bad := range []string{"hackmd", "@profile", "SUMMARY", "5+ years of experience"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("claims should not contain header or summary noise %q: %#v", bad, claims)
		}
	}
	if !strings.Contains(joined, "SecOps platform") || !strings.Contains(joined, "SRE agent") {
		t.Fatalf("claims should include concrete work bullets, got %#v", claims)
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
	if _, err := ExtractDocumentText("cv.pdf", []byte("not actually a pdf")); err == nil || !strings.Contains(err.Error(), "does not look like a valid PDF") {
		t.Fatalf("expected friendly invalid PDF error, got %v", err)
	}
	normalized, err := normalizePDFData("cv.pdf", []byte("prefix\n%PDF-1.7\nbody"))
	if err != nil {
		t.Fatalf("expected prefixed PDF data to normalize: %v", err)
	}
	if !bytes.HasPrefix(normalized, []byte("%PDF-")) {
		t.Fatalf("expected normalized PDF to start with header, got %q", string(normalized[:min(len(normalized), 8)]))
	}

	pdfText := normalizeExtractedPDFText("SUMMARYSite Reliability Engineer.WORK EXPERIENCEShopeeMain responsibilities:•Designed Kubernetes clustersand WAF automation.Technologies used: Kubernetes.")
	if !strings.Contains(pdfText, "\n• Designed Kubernetes clusters and WAF automation.") {
		t.Fatalf("expected PDF text normalization to preserve bullet boundaries, got %q", pdfText)
	}
	if strings.Contains(pdfText, "SUMMARYSite") || strings.Contains(pdfText, "responsibilities:•") {
		t.Fatalf("expected PDF text normalization to separate glued sections, got %q", pdfText)
	}
	if strings.Contains(pdfText, "clustersand") {
		t.Fatalf("expected PDF text normalization to repair common joined words, got %q", pdfText)
	}
}

func TestUploadPolicies(t *testing.T) {
	ctx := context.Background()
	jdImage := uploadHeader(t, "jd_file", "job.png", "image/png", []byte{0x89, 'P', 'N', 'G'})
	jdText, err := ExtractJDUploadText(ctx, fakeAI{imageText: "SRE job from screenshot"}, jdImage, "")
	if err != nil {
		t.Fatalf("expected JD image to be accepted: %v", err)
	}
	if jdText != "SRE job from screenshot" {
		t.Fatalf("unexpected JD image text: %q", jdText)
	}

	jdTXT := uploadHeader(t, "jd_file", "job.txt", "text/plain", []byte("SRE role"))
	if _, err := ExtractJDUploadText(ctx, fakeAI{}, jdTXT, ""); err != nil {
		t.Fatalf("expected JD txt to be accepted: %v", err)
	}

	cvMD := uploadHeader(t, "cv_file", "cv.md", "text/markdown", []byte("# CV"))
	if _, err := ExtractCVUploadText(ctx, cvMD, ""); err != nil {
		t.Fatalf("expected CV md to be accepted: %v", err)
	}

	cvTXT := uploadHeader(t, "cv_file", "cv.txt", "text/plain", []byte("CV"))
	if _, err := ExtractCVUploadText(ctx, cvTXT, ""); err == nil {
		t.Fatal("expected CV txt upload to be rejected")
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

func uploadHeader(t *testing.T, field string, fileName string, contentType string, data []byte) *multipart.FileHeader {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="`+field+`"; filename="`+fileName+`"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, "/upload", &body)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if err := req.ParseMultipartForm(maxDocumentBytes); err != nil {
		t.Fatalf("parse multipart form: %v", err)
	}
	return req.MultipartForm.File[field][0]
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
