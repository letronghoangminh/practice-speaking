package services

import (
	"context"

	"practice-speaking/backend/internal/models"
)

type AIClient interface {
	GenerateBaseline(ctx context.Context, input BaselineInput) (BaselinePlan, error)
	EvaluateAnswer(ctx context.Context, input TurnInput) (TurnEvaluation, error)
	GenerateFinalReport(ctx context.Context, input FinalReportInput) (FinalReport, error)
	ExtractImageText(ctx context.Context, fileName string, contentType string, image []byte) (string, error)
	Transcribe(ctx context.Context, fileName string, contentType string, audio []byte) (string, error)
	Synthesize(ctx context.Context, text string) (AudioResult, error)
}

type BaselineInput struct {
	Mode   string `json:"mode"`
	JDText string `json:"jd_text,omitempty"`
	CVText string `json:"cv_text,omitempty"`
}

type BaselinePlan struct {
	Topics []BaselineTopic `json:"topics"`
}

type BaselineTopic struct {
	Title           string `json:"title"`
	Category        string `json:"category"`
	QuestionType    string `json:"question_type"`
	InitialQuestion string `json:"initial_question"`
}

type TurnInput struct {
	Session         models.Session `json:"session"`
	Topic           models.Topic   `json:"topic"`
	Question        string         `json:"question"`
	Transcript      string         `json:"transcript"`
	AnsweredTurns   []models.Turn  `json:"answered_turns"`
	RemainingTopics []models.Topic `json:"remaining_topics"`
}

type TurnEvaluation struct {
	TranscriptSummary string   `json:"transcript_summary"`
	TechnicalScore    float64  `json:"technical_score"`
	EnglishScore      float64  `json:"english_score"`
	Strengths         []string `json:"strengths"`
	Improvements      []string `json:"improvements"`
	EnglishNotes      []string `json:"english_notes"`
	TechnicalNotes    []string `json:"technical_notes"`
	ReferenceAnswer   string   `json:"reference_answer"`
	ShouldFollowUp    bool     `json:"should_follow_up"`
	FollowUpQuestion  string   `json:"follow_up_question"`
	NextQuestionType  string   `json:"next_question_type"`
}

type FinalReportInput struct {
	Session models.Session `json:"session"`
	Topics  []models.Topic `json:"topics"`
	Turns   []models.Turn  `json:"turns"`
}

type FinalReport struct {
	Summary                 string         `json:"summary"`
	OverallScore            float64        `json:"overall_score"`
	TechnicalScore          float64        `json:"technical_score"`
	EnglishScore            float64        `json:"english_score"`
	EnglishImprovementAreas []string       `json:"english_improvement_areas"`
	TechnicalSkillGaps      []string       `json:"technical_skill_gaps"`
	RecommendedPractice     []string       `json:"recommended_practice"`
	Answers                 []ReportAnswer `json:"answers"`
}

type ReportAnswer struct {
	Question        string  `json:"question"`
	Transcript      string  `json:"transcript"`
	ReferenceAnswer string  `json:"reference_answer"`
	Score           float64 `json:"score"`
}

type AudioResult struct {
	Bytes []byte `json:"-"`
	MIME  string `json:"mime"`
}

type FeedbackPayload struct {
	Strengths      []string `json:"strengths"`
	Improvements   []string `json:"improvements"`
	EnglishNotes   []string `json:"english_notes"`
	TechnicalNotes []string `json:"technical_notes"`
}

type SessionEnvelope struct {
	Session         models.Session `json:"session"`
	CurrentQuestion *models.Turn   `json:"current_question,omitempty"`
	AudioBase64     string         `json:"audio_base64,omitempty"`
	AudioMIME       string         `json:"audio_mime,omitempty"`
}
