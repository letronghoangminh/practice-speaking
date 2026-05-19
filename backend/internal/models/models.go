package models

import (
	"time"

	"gorm.io/datatypes"
)

const (
	SessionModeInterview = "interview"
	SessionModePractice  = "practice"

	SessionStatusActive    = "active"
	SessionStatusCompleted = "completed"

	QuestionTypeTheory   = "theory"
	QuestionTypePractice = "practice"
	QuestionTypeScenario = "scenario"
)

type Session struct {
	ID                   uint           `gorm:"primaryKey" json:"id"`
	Mode                 string         `gorm:"size:32;not null;index" json:"mode"`
	Status               string         `gorm:"size:32;not null;index" json:"status"`
	StartedAt            time.Time      `gorm:"not null" json:"started_at"`
	DeadlineAt           time.Time      `gorm:"not null" json:"deadline_at"`
	CompletedAt          *time.Time     `json:"completed_at,omitempty"`
	JDText               string         `gorm:"type:text" json:"jd_text,omitempty"`
	CVText               string         `gorm:"type:text" json:"cv_text,omitempty"`
	CurrentTopicID       *uint          `json:"current_topic_id,omitempty"`
	LastOpenAIResponseID string         `gorm:"size:128" json:"last_openai_response_id,omitempty"`
	OverallScore         float64        `json:"overall_score"`
	TechnicalScore       float64        `json:"technical_score"`
	EnglishScore         float64        `json:"english_score"`
	FinalReport          datatypes.JSON `gorm:"type:jsonb" json:"final_report,omitempty"`
	Topics               []Topic        `json:"topics,omitempty"`
	Turns                []Turn         `json:"turns,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type Topic struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	SessionID       uint      `gorm:"not null;index" json:"session_id"`
	Title           string    `gorm:"size:160;not null" json:"title"`
	Category        string    `gorm:"size:80;not null" json:"category"`
	OpeningQuestion string    `gorm:"type:text" json:"opening_question,omitempty"`
	OrderIndex      int       `gorm:"not null" json:"order_index"`
	AskedCount      int       `gorm:"not null;default:0" json:"asked_count"`
	MaxQuestions    int       `gorm:"not null;default:3" json:"max_questions"`
	Completed       bool      `gorm:"not null;default:false" json:"completed"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Turn struct {
	ID                uint           `gorm:"primaryKey" json:"id"`
	SessionID         uint           `gorm:"not null;index" json:"session_id"`
	TopicID           uint           `gorm:"not null;index" json:"topic_id"`
	QuestionText      string         `gorm:"type:text;not null" json:"question_text"`
	QuestionType      string         `gorm:"size:32;not null" json:"question_type"`
	Transcript        string         `gorm:"type:text" json:"transcript,omitempty"`
	TranscriptSummary string         `gorm:"type:text" json:"transcript_summary,omitempty"`
	TechnicalScore    float64        `json:"technical_score"`
	EnglishScore      float64        `json:"english_score"`
	OverallScore      float64        `json:"overall_score"`
	Feedback          datatypes.JSON `gorm:"type:jsonb" json:"feedback,omitempty"`
	ReferenceAnswer   string         `gorm:"type:text" json:"reference_answer,omitempty"`
	IsFollowUp        bool           `gorm:"not null;default:false" json:"is_follow_up"`
	AnsweredAt        *time.Time     `json:"answered_at,omitempty"`
	SkippedAt         *time.Time     `json:"skipped_at,omitempty"`
	SkipReason        string         `gorm:"size:120" json:"skip_reason,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}
