package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"strings"
	"time"

	"practice-speaking/backend/internal/models"

	"github.com/redis/go-redis/v9"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	interviewDuration = 20 * time.Minute
	practiceDuration  = 10 * time.Minute
	maxTopicQuestions = 3
)

type SessionService struct {
	db    *gorm.DB
	cache *redis.Client
	ai    AIClient
	now   func() time.Time
}

type CreateSessionInput struct {
	Mode   string
	JDText string
	CVText string
	JDFile *multipart.FileHeader
	CVFile *multipart.FileHeader
}

func NewSessionService(db *gorm.DB, cache *redis.Client, ai AIClient) *SessionService {
	return &SessionService{
		db:    db,
		cache: cache,
		ai:    ai,
		now:   time.Now,
	}
}

func (s *SessionService) CreateSession(ctx context.Context, input CreateSessionInput) (SessionEnvelope, error) {
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode != models.SessionModeInterview && mode != models.SessionModePractice {
		return SessionEnvelope{}, validationError("mode must be %q or %q", models.SessionModeInterview, models.SessionModePractice)
	}

	jdText, err := ExtractJDUploadText(ctx, s.ai, input.JDFile, input.JDText)
	if err != nil {
		return SessionEnvelope{}, validationError("JD file: %s", err.Error())
	}
	cvText, err := ExtractCVUploadText(ctx, input.CVFile, input.CVText)
	if err != nil {
		return SessionEnvelope{}, validationError("CV file: %s", err.Error())
	}
	if mode == models.SessionModeInterview && (strings.TrimSpace(jdText) == "" || strings.TrimSpace(cvText) == "") {
		return SessionEnvelope{}, validationError("interview mode needs both a JD and CV upload or pasted text")
	}

	plan, err := s.ai.GenerateBaseline(ctx, BaselineInput{Mode: mode, JDText: jdText, CVText: cvText})
	if err != nil {
		return SessionEnvelope{}, fmt.Errorf("generate baseline: %w", err)
	}
	plan = normalizeBaseline(plan)
	if len(plan.Topics) == 0 {
		return SessionEnvelope{}, fmt.Errorf("generate baseline: no topics returned")
	}

	now := s.now().UTC()
	duration := practiceDuration
	if mode == models.SessionModeInterview {
		duration = interviewDuration
	}

	session := models.Session{
		Mode:       mode,
		Status:     models.SessionStatusActive,
		StartedAt:  now,
		DeadlineAt: now.Add(duration),
		JDText:     jdText,
		CVText:     cvText,
	}
	var firstTurn models.Turn

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&session).Error; err != nil {
			return err
		}

		for idx, topic := range plan.Topics {
			record := models.Topic{
				SessionID:       session.ID,
				Title:           topic.Title,
				Category:        topic.Category,
				OpeningQuestion: topic.InitialQuestion,
				OrderIndex:      idx,
				MaxQuestions:    maxTopicQuestions,
			}
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
			if idx == 0 {
				session.CurrentTopicID = &record.ID
				firstTurn = models.Turn{
					SessionID:    session.ID,
					TopicID:      record.ID,
					QuestionText: topic.InitialQuestion,
					QuestionType: topic.QuestionType,
				}
			}
		}

		if err := tx.Save(&session).Error; err != nil {
			return err
		}
		return tx.Create(&firstTurn).Error
	})
	if err != nil {
		return SessionEnvelope{}, err
	}

	audio, _ := s.ai.Synthesize(ctx, firstTurn.QuestionText)
	return s.GetSession(ctx, session.ID, audio)
}

func (s *SessionService) ListSessions(ctx context.Context) ([]models.Session, error) {
	var sessions []models.Session
	err := s.db.WithContext(ctx).
		Preload("Topics", func(db *gorm.DB) *gorm.DB { return db.Order("order_index asc") }).
		Preload("Turns", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		Order("created_at desc").
		Find(&sessions).Error
	return sessions, err
}

func (s *SessionService) GetSession(ctx context.Context, id uint, audio AudioResult) (SessionEnvelope, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		return SessionEnvelope{}, err
	}
	var current *models.Turn
	if session.Status == models.SessionStatusActive {
		current = currentQuestion(session.Turns)
	}
	envelope := SessionEnvelope{Session: session, CurrentQuestion: current}
	if len(audio.Bytes) > 0 {
		envelope.AudioBase64 = base64.StdEncoding.EncodeToString(audio.Bytes)
		envelope.AudioMIME = audio.MIME
	}
	return envelope, nil
}

func (s *SessionService) SubmitTextAnswer(ctx context.Context, id uint, answer string) (SessionEnvelope, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return SessionEnvelope{}, validationError("answer cannot be empty")
	}
	return s.submitTranscript(ctx, id, answer)
}

func (s *SessionService) SubmitAudioAnswer(ctx context.Context, id uint, fileName string, contentType string, audio []byte) (SessionEnvelope, error) {
	if len(audio) == 0 {
		return SessionEnvelope{}, validationError("audio file cannot be empty")
	}
	transcript, err := s.ai.Transcribe(ctx, fileName, contentType, audio)
	if err != nil {
		return SessionEnvelope{}, fmt.Errorf("transcribe answer: %w", err)
	}
	return s.submitTranscript(ctx, id, transcript)
}

func (s *SessionService) SkipCurrentQuestion(ctx context.Context, id uint) (SessionEnvelope, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		return SessionEnvelope{}, err
	}
	if session.Status != models.SessionStatusActive {
		return SessionEnvelope{}, conflictError("session is already completed")
	}
	if !session.DeadlineAt.After(s.now().UTC()) {
		if err := s.finalizeLoaded(ctx, &session); err != nil {
			return SessionEnvelope{}, err
		}
		return s.GetSession(ctx, id, AudioResult{})
	}

	pending := currentQuestion(session.Turns)
	if pending == nil {
		return SessionEnvelope{}, conflictError("session does not have a pending question")
	}
	topic, ok := findTopic(session.Topics, pending.TopicID)
	if !ok {
		return SessionEnvelope{}, notFoundError("current topic was not found")
	}

	var nextQuestionText string
	var nextQuestionTopicID uint
	var shouldCreateNext bool
	now := s.now().UTC()

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var turn models.Turn
		if err := tx.First(&turn, pending.ID).Error; err != nil {
			return err
		}
		if turn.AnsweredAt != nil {
			return conflictError("current question is already answered")
		}
		if turn.SkippedAt != nil {
			return conflictError("current question is already skipped")
		}
		turn.SkippedAt = &now
		turn.SkipReason = "unknown_topic"
		if err := tx.Save(&turn).Error; err != nil {
			return err
		}

		var topicRecord models.Topic
		if err := tx.First(&topicRecord, topic.ID).Error; err != nil {
			return err
		}
		topicRecord.Completed = true

		nextTopic, found, err := nextTopic(tx, session.ID, topicRecord.ID)
		if err != nil {
			return err
		}
		if found {
			nextQuestionText = nextTopic.OpeningQuestion
			nextQuestionTopicID = nextTopic.ID
			shouldCreateNext = true
			session.CurrentTopicID = &nextQuestionTopicID
		} else {
			session.CurrentTopicID = nil
		}

		if err := tx.Save(&topicRecord).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Session{}).Where("id = ?", session.ID).Update("current_topic_id", session.CurrentTopicID).Error; err != nil {
			return err
		}
		if shouldCreateNext {
			nextTurn := models.Turn{
				SessionID:    session.ID,
				TopicID:      nextQuestionTopicID,
				QuestionText: nextQuestionText,
				QuestionType: models.QuestionTypeScenario,
			}
			return tx.Create(&nextTurn).Error
		}
		return nil
	})
	if err != nil {
		return SessionEnvelope{}, err
	}

	if !shouldCreateNext || !session.DeadlineAt.After(s.now().UTC()) {
		if err := s.finalizeByID(ctx, id); err != nil {
			return SessionEnvelope{}, err
		}
		return s.GetSession(ctx, id, AudioResult{})
	}

	audio, _ := s.ai.Synthesize(ctx, nextQuestionText)
	return s.GetSession(ctx, id, audio)
}

func (s *SessionService) FinalizeSession(ctx context.Context, id uint) (SessionEnvelope, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		return SessionEnvelope{}, err
	}
	if session.Status == models.SessionStatusCompleted {
		return SessionEnvelope{Session: session}, nil
	}
	if err := s.finalizeLoaded(ctx, &session); err != nil {
		return SessionEnvelope{}, err
	}
	return s.GetSession(ctx, id, AudioResult{})
}

func (s *SessionService) GetReport(ctx context.Context, id uint) (FinalReport, error) {
	cacheKey := fmt.Sprintf("session:%d:report", id)
	if s.cache != nil {
		if cached, err := s.cache.Get(ctx, cacheKey).Result(); err == nil && cached != "" {
			var report FinalReport
			if json.Unmarshal([]byte(cached), &report) == nil {
				return report, nil
			}
		}
	}

	session, err := s.loadSession(ctx, id)
	if err != nil {
		return FinalReport{}, err
	}
	if session.Status != models.SessionStatusCompleted {
		return FinalReport{}, conflictError("session is still active")
	}
	var report FinalReport
	if len(session.FinalReport) == 0 {
		return FinalReport{}, notFoundError("session report is not available")
	}
	if err := json.Unmarshal(session.FinalReport, &report); err != nil {
		return FinalReport{}, fmt.Errorf("decode report: %w", err)
	}
	return report, nil
}

func (s *SessionService) submitTranscript(ctx context.Context, id uint, transcript string) (SessionEnvelope, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		return SessionEnvelope{}, err
	}
	if session.Status != models.SessionStatusActive {
		return SessionEnvelope{}, conflictError("session is already completed")
	}
	if !session.DeadlineAt.After(s.now().UTC()) {
		if err := s.finalizeLoaded(ctx, &session); err != nil {
			return SessionEnvelope{}, err
		}
		return s.GetSession(ctx, id, AudioResult{})
	}

	pending := currentQuestion(session.Turns)
	if pending == nil {
		return SessionEnvelope{}, conflictError("session does not have a pending question")
	}
	topic, ok := findTopic(session.Topics, pending.TopicID)
	if !ok {
		return SessionEnvelope{}, notFoundError("current topic was not found")
	}

	answeredTurns := answeredTurns(session.Turns)
	remainingTopics := remainingTopics(session.Topics, topic.ID)
	evaluation, err := s.ai.EvaluateAnswer(ctx, TurnInput{
		Session:         session,
		Topic:           topic,
		Question:        pending.QuestionText,
		Transcript:      transcript,
		AnsweredTurns:   answeredTurns,
		RemainingTopics: remainingTopics,
	})
	if err != nil {
		return SessionEnvelope{}, fmt.Errorf("evaluate answer: %w", err)
	}

	var nextQuestionText string
	var nextQuestionType string
	var shouldCreateNext bool
	var nextIsFollowUp bool

	now := s.now().UTC()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var turn models.Turn
		if err := tx.First(&turn, pending.ID).Error; err != nil {
			return err
		}
		if turn.SkippedAt != nil {
			return conflictError("current question is already skipped")
		}
		var topicRecord models.Topic
		if err := tx.First(&topicRecord, topic.ID).Error; err != nil {
			return err
		}

		feedback, err := json.Marshal(FeedbackPayload{
			Strengths:      evaluation.Strengths,
			Improvements:   evaluation.Improvements,
			EnglishNotes:   evaluation.EnglishNotes,
			TechnicalNotes: evaluation.TechnicalNotes,
		})
		if err != nil {
			return err
		}

		turn.Transcript = transcript
		turn.TranscriptSummary = evaluation.TranscriptSummary
		turn.TechnicalScore = clampScore(evaluation.TechnicalScore)
		turn.EnglishScore = clampScore(evaluation.EnglishScore)
		turn.OverallScore = clampScore(turn.TechnicalScore*0.6 + turn.EnglishScore*0.4)
		turn.Feedback = datatypes.JSON(feedback)
		turn.ReferenceAnswer = strings.TrimSpace(evaluation.ReferenceAnswer)
		turn.AnsweredAt = &now
		if err := tx.Save(&turn).Error; err != nil {
			return err
		}

		topicRecord.AskedCount++
		canAskFollowUp := evaluation.ShouldFollowUp && topicRecord.AskedCount < topicRecord.MaxQuestions && strings.TrimSpace(evaluation.FollowUpQuestion) != ""
		if canAskFollowUp {
			nextQuestionText = strings.TrimSpace(evaluation.FollowUpQuestion)
			nextQuestionType = normalizeQuestionType(evaluation.NextQuestionType)
			shouldCreateNext = true
			nextIsFollowUp = true
		} else {
			topicRecord.Completed = true
			nextTopic, found, err := nextTopic(tx, session.ID, topicRecord.ID)
			if err != nil {
				return err
			}
			if found {
				nextQuestionText = nextTopic.OpeningQuestion
				nextQuestionType = models.QuestionTypeScenario
				shouldCreateNext = true
				session.CurrentTopicID = &nextTopic.ID
			} else {
				session.CurrentTopicID = nil
			}
		}

		if err := tx.Save(&topicRecord).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Session{}).Where("id = ?", session.ID).Update("current_topic_id", session.CurrentTopicID).Error; err != nil {
			return err
		}
		if shouldCreateNext {
			nextTurn := models.Turn{
				SessionID:    session.ID,
				TopicID:      deref(session.CurrentTopicID, topicRecord.ID),
				QuestionText: nextQuestionText,
				QuestionType: nextQuestionType,
				IsFollowUp:   nextIsFollowUp,
			}
			if nextIsFollowUp {
				nextTurn.TopicID = topicRecord.ID
			}
			return tx.Create(&nextTurn).Error
		}
		return nil
	})
	if err != nil {
		return SessionEnvelope{}, err
	}

	if !shouldCreateNext || !session.DeadlineAt.After(s.now().UTC()) {
		if err := s.finalizeByID(ctx, id); err != nil {
			return SessionEnvelope{}, err
		}
		return s.GetSession(ctx, id, AudioResult{})
	}

	audio, _ := s.ai.Synthesize(ctx, nextQuestionText)
	return s.GetSession(ctx, id, audio)
}

func (s *SessionService) finalizeByID(ctx context.Context, id uint) error {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		return err
	}
	if session.Status == models.SessionStatusCompleted {
		return nil
	}
	return s.finalizeLoaded(ctx, &session)
}

func (s *SessionService) finalizeLoaded(ctx context.Context, session *models.Session) error {
	if session.Status == models.SessionStatusCompleted {
		return nil
	}
	answered := answeredTurns(session.Turns)
	report, err := s.ai.GenerateFinalReport(ctx, FinalReportInput{Session: *session, Topics: session.Topics, Turns: answered})
	if err != nil {
		return fmt.Errorf("generate final report: %w", err)
	}
	technical, english, overall := AggregateScores(session.Turns)
	if report.TechnicalScore == 0 {
		report.TechnicalScore = technical
	}
	if report.EnglishScore == 0 {
		report.EnglishScore = english
	}
	if report.OverallScore == 0 {
		report.OverallScore = overall
	}
	reportBytes, err := json.Marshal(report)
	if err != nil {
		return err
	}

	now := s.now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Model(&models.Session{}).Where("id = ?", session.ID).Updates(map[string]any{
			"status":           models.SessionStatusCompleted,
			"completed_at":     &now,
			"current_topic_id": nil,
			"technical_score":  report.TechnicalScore,
			"english_score":    report.EnglishScore,
			"overall_score":    report.OverallScore,
			"final_report":     datatypes.JSON(reportBytes),
		}).Error
		if err != nil {
			return err
		}
		if s.cache != nil {
			key := fmt.Sprintf("session:%d:report", session.ID)
			_ = s.cache.Set(ctx, key, string(reportBytes), 24*time.Hour).Err()
		}
		return nil
	})
}

func (s *SessionService) loadSession(ctx context.Context, id uint) (models.Session, error) {
	var session models.Session
	err := s.db.WithContext(ctx).
		Preload("Topics", func(db *gorm.DB) *gorm.DB { return db.Order("order_index asc") }).
		Preload("Turns", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		First(&session, id).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return models.Session{}, notFoundError("session %d was not found", id)
		}
		return models.Session{}, err
	}
	return session, nil
}

func currentQuestion(turns []models.Turn) *models.Turn {
	for idx := len(turns) - 1; idx >= 0; idx-- {
		if turns[idx].AnsweredAt == nil && turns[idx].SkippedAt == nil {
			turn := turns[idx]
			return &turn
		}
	}
	return nil
}

func answeredTurns(turns []models.Turn) []models.Turn {
	out := make([]models.Turn, 0, len(turns))
	for _, turn := range turns {
		if turn.AnsweredAt != nil {
			out = append(out, turn)
		}
	}
	return out
}

func remainingTopics(topics []models.Topic, currentTopicID uint) []models.Topic {
	out := make([]models.Topic, 0, len(topics))
	for _, topic := range topics {
		if !topic.Completed && topic.ID != currentTopicID {
			out = append(out, topic)
		}
	}
	return out
}

func findTopic(topics []models.Topic, id uint) (models.Topic, bool) {
	for _, topic := range topics {
		if topic.ID == id {
			return topic, true
		}
	}
	return models.Topic{}, false
}

func nextTopic(tx *gorm.DB, sessionID uint, currentID uint) (models.Topic, bool, error) {
	var current models.Topic
	if err := tx.First(&current, currentID).Error; err != nil {
		return models.Topic{}, false, err
	}
	var topic models.Topic
	err := tx.Where("session_id = ? AND completed = ? AND id <> ? AND order_index > ?", sessionID, false, currentID, current.OrderIndex).
		Order("order_index asc").
		First(&topic).Error
	if err == nil {
		return topic, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return models.Topic{}, false, nil
	}
	return models.Topic{}, false, err
}

func deref(value *uint, fallback uint) uint {
	if value == nil {
		return fallback
	}
	return *value
}
