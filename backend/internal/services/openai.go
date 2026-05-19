package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"practice-speaking/backend/internal/config"
	"practice-speaking/backend/internal/models"
)

type OpenAIClient struct {
	apiKey    string
	baseURL   string
	textModel string
	sttModel  string
	ttsModel  string
	ttsVoice  string
	client    *http.Client
}

func NewOpenAIClient(cfg config.Config) *OpenAIClient {
	timeoutSeconds, err := strconv.Atoi(cfg.OpenAITimeoutSec)
	if err != nil || timeoutSeconds <= 0 {
		timeoutSeconds = 90
	}

	return &OpenAIClient{
		apiKey:    cfg.OpenAIAPIKey,
		baseURL:   strings.TrimRight(cfg.OpenAIBaseURL, "/"),
		textModel: cfg.OpenAITextModel,
		sttModel:  cfg.OpenAISTTModel,
		ttsModel:  cfg.OpenAITTSModel,
		ttsVoice:  cfg.OpenAITTSVoice,
		client:    &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second},
	}
}

func (c *OpenAIClient) GenerateBaseline(ctx context.Context, input BaselineInput) (BaselinePlan, error) {
	if c.apiKey == "" {
		return localBaseline(input), nil
	}

	var plan BaselinePlan
	err := c.responsesJSON(ctx, "baseline_plan", baselineSchema(), baselineSystemPrompt(), baselineUserPrompt(input), &plan)
	if err != nil {
		return BaselinePlan{}, err
	}
	if len(plan.Topics) == 0 {
		return localBaseline(input), nil
	}
	return normalizeBaseline(plan), nil
}

func (c *OpenAIClient) EvaluateAnswer(ctx context.Context, input TurnInput) (TurnEvaluation, error) {
	if c.apiKey == "" {
		return localEvaluate(input), nil
	}

	var evaluation TurnEvaluation
	err := c.responsesJSON(ctx, "turn_evaluation", turnEvaluationSchema(), interviewerSystemPrompt(), turnUserPrompt(input), &evaluation)
	if err != nil {
		return TurnEvaluation{}, err
	}
	evaluation.TechnicalScore = clampScore(evaluation.TechnicalScore)
	evaluation.EnglishScore = clampScore(evaluation.EnglishScore)
	evaluation.NextQuestionType = normalizeQuestionType(evaluation.NextQuestionType)
	if strings.TrimSpace(evaluation.ReferenceAnswer) == "" {
		evaluation.ReferenceAnswer = "A strong answer should explain the tradeoff, name the operational risk, and describe how you would validate the result in production."
	}
	return evaluation, nil
}

func (c *OpenAIClient) GenerateFinalReport(ctx context.Context, input FinalReportInput) (FinalReport, error) {
	if c.apiKey == "" {
		return localFinalReport(input), nil
	}

	var report FinalReport
	err := c.responsesJSON(ctx, "final_report", finalReportSchema(), finalReportSystemPrompt(), finalReportUserPrompt(input), &report)
	if err != nil {
		return FinalReport{}, err
	}
	report.OverallScore = clampScore(report.OverallScore)
	report.TechnicalScore = clampScore(report.TechnicalScore)
	report.EnglishScore = clampScore(report.EnglishScore)
	if strings.TrimSpace(report.Summary) == "" {
		fallback := localFinalReport(input)
		report.Summary = fallback.Summary
	}
	return report, nil
}

func (c *OpenAIClient) Transcribe(ctx context.Context, fileName string, contentType string, audio []byte) (string, error) {
	if c.apiKey == "" {
		return "Local fallback transcript: I would clarify the production symptoms, inspect metrics and logs, form a hypothesis, apply the smallest safe mitigation, and then verify recovery with SLO and user-impact signals.", nil
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", c.sttModel); err != nil {
		return "", err
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	part, err := writer.CreateFormFile("file", filepath.Base(fileName))
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	c.authorize(req)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if contentType != "" {
		req.Header.Set("X-Input-Audio-Type", contentType)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", apiError("transcribe audio", resp)
	}

	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode transcription response: %w", err)
	}
	if strings.TrimSpace(parsed.Text) == "" {
		return "", fmt.Errorf("transcription was empty")
	}
	return strings.TrimSpace(parsed.Text), nil
}

func (c *OpenAIClient) Synthesize(ctx context.Context, text string) (AudioResult, error) {
	text = strings.TrimSpace(text)
	if c.apiKey == "" || text == "" {
		return AudioResult{}, nil
	}

	payload := map[string]any{
		"model":           c.ttsModel,
		"voice":           c.ttsVoice,
		"input":           text,
		"response_format": "mp3",
		"instructions":    "Speak like a calm senior technical interviewer. Use natural pacing and clear English pronunciation.",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return AudioResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return AudioResult{}, err
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return AudioResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return AudioResult{}, apiError("synthesize speech", resp)
	}
	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return AudioResult{}, err
	}
	return AudioResult{Bytes: audio, MIME: "audio/mpeg"}, nil
}

func (c *OpenAIClient) responsesJSON(ctx context.Context, schemaName string, schema map[string]any, systemPrompt string, userPrompt string, target any) error {
	payload := map[string]any{
		"model": c.textModel,
		"input": []map[string]any{
			{
				"role": "system",
				"content": []map[string]string{
					{"type": "input_text", "text": systemPrompt},
				},
			},
			{
				"role": "user",
				"content": []map[string]string{
					{"type": "input_text", "text": userPrompt},
				},
			},
		},
		"reasoning": map[string]string{"effort": "low"},
		"text": map[string]any{
			"verbosity": "low",
			"format": map[string]any{
				"type":   "json_schema",
				"name":   schemaName,
				"schema": schema,
				"strict": true,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return apiError("create response", resp)
	}

	var envelope responsesEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	text := envelope.Text()
	if text == "" {
		return fmt.Errorf("model response did not include text output")
	}
	if err := ParseJSONPayload(text, target); err != nil {
		return fmt.Errorf("parse model JSON: %w", err)
	}
	return nil
}

func (c *OpenAIClient) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}

type responsesEnvelope struct {
	ID         string `json:"id"`
	OutputText string `json:"output_text"`
	Output     []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func (e responsesEnvelope) Text() string {
	if strings.TrimSpace(e.OutputText) != "" {
		return strings.TrimSpace(e.OutputText)
	}
	for _, item := range e.Output {
		for _, content := range item.Content {
			if strings.TrimSpace(content.Text) != "" {
				return strings.TrimSpace(content.Text)
			}
		}
	}
	return ""
}

func apiError(action string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("%s failed with status %d: %s", action, resp.StatusCode, strings.TrimSpace(string(body)))
}

func ParseJSONPayload(raw string, target any) error {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if err := json.Unmarshal([]byte(cleaned), target); err == nil {
		return nil
	}

	start := strings.IndexAny(cleaned, "{[")
	endObj := strings.LastIndex(cleaned, "}")
	endArr := strings.LastIndex(cleaned, "]")
	end := max(endObj, endArr)
	if start >= 0 && end > start {
		return json.Unmarshal([]byte(cleaned[start:end+1]), target)
	}
	return json.Unmarshal([]byte(cleaned), target)
}

func baselineSystemPrompt() string {
	return "You are a senior SRE interviewer. Create realistic interview topics for a DevOps/SRE candidate. Return JSON only."
}

func baselineUserPrompt(input BaselineInput) string {
	topicGuidance := "Create 8 to 12 role-aligned topics from the JD and CV. Cover theory, practical operations, and scenario questions."
	if input.Mode == models.SessionModePractice {
		topicGuidance = "Create 12 to 16 varied DevOps, SRE, and platform engineering topics. Include Terraform, Helm, Kubernetes, cloud operations, CI/CD, GitOps, monitoring, logging, incident response, SLOs, security, and platform engineering."
	}
	return fmt.Sprintf(`Mode: %s

Job description:
%s

Candidate CV:
%s

%s Keep each initial question concise and realistic.`, input.Mode, trimForPrompt(input.JDText, 6000), trimForPrompt(input.CVText, 6000), topicGuidance)
}

func interviewerSystemPrompt() string {
	return "You are a realistic senior DevOps/SRE technical interviewer and English communication coach. Score fairly, keep feedback concise, and ask at most one follow-up."
}

func turnUserPrompt(input TurnInput) string {
	turns, _ := json.Marshal(input.AnsweredTurns)
	remaining, _ := json.Marshal(input.RemainingTopics)
	return fmt.Sprintf(`Session mode: %s
Current topic: %s (%s)
Question: %s
Candidate transcript: %s
Answered turns JSON: %s
Remaining topics JSON: %s

Evaluate the answer for technical correctness and English clarity. Decide whether one follow-up is useful. The application will enforce the maximum number of follow-ups.`, input.Session.Mode, input.Topic.Title, input.Topic.Category, input.Question, input.Transcript, turns, remaining)
}

func finalReportSystemPrompt() string {
	return "You are a concise SRE interview assessor. Produce a final report that is direct, useful, and technically accurate. Return JSON only."
}

func finalReportUserPrompt(input FinalReportInput) string {
	turns, _ := json.Marshal(input.Turns)
	topics, _ := json.Marshal(input.Topics)
	return fmt.Sprintf(`Session mode: %s
Topics JSON: %s
Turns JSON: %s

Summarize the candidate performance. Include English skill improvement areas, technical gaps, recommended practice, and concise reference answers for each asked question.`, input.Session.Mode, topics, turns)
}

func baselineSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"topics": map[string]any{
				"type":     "array",
				"minItems": 6,
				"maxItems": 16,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":            map[string]string{"type": "string"},
						"category":         map[string]string{"type": "string"},
						"question_type":    map[string]any{"type": "string", "enum": []string{models.QuestionTypeTheory, models.QuestionTypePractice, models.QuestionTypeScenario}},
						"initial_question": map[string]string{"type": "string"},
					},
					"required":             []string{"title", "category", "question_type", "initial_question"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"topics"},
		"additionalProperties": false,
	}
}

func turnEvaluationSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"transcript_summary": map[string]string{"type": "string"},
			"technical_score":    map[string]string{"type": "number"},
			"english_score":      map[string]string{"type": "number"},
			"strengths":          stringArraySchema(),
			"improvements":       stringArraySchema(),
			"english_notes":      stringArraySchema(),
			"technical_notes":    stringArraySchema(),
			"reference_answer":   map[string]string{"type": "string"},
			"should_follow_up":   map[string]string{"type": "boolean"},
			"follow_up_question": map[string]string{"type": "string"},
			"next_question_type": map[string]any{"type": "string", "enum": []string{models.QuestionTypeTheory, models.QuestionTypePractice, models.QuestionTypeScenario}},
		},
		"required":             []string{"transcript_summary", "technical_score", "english_score", "strengths", "improvements", "english_notes", "technical_notes", "reference_answer", "should_follow_up", "follow_up_question", "next_question_type"},
		"additionalProperties": false,
	}
}

func finalReportSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary":                   map[string]string{"type": "string"},
			"overall_score":             map[string]string{"type": "number"},
			"technical_score":           map[string]string{"type": "number"},
			"english_score":             map[string]string{"type": "number"},
			"english_improvement_areas": stringArraySchema(),
			"technical_skill_gaps":      stringArraySchema(),
			"recommended_practice":      stringArraySchema(),
			"answers": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"question":         map[string]string{"type": "string"},
						"transcript":       map[string]string{"type": "string"},
						"reference_answer": map[string]string{"type": "string"},
						"score":            map[string]string{"type": "number"},
					},
					"required":             []string{"question", "transcript", "reference_answer", "score"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"summary", "overall_score", "technical_score", "english_score", "english_improvement_areas", "technical_skill_gaps", "recommended_practice", "answers"},
		"additionalProperties": false,
	}
}

func stringArraySchema() map[string]any {
	return map[string]any{
		"type":  "array",
		"items": map[string]string{"type": "string"},
	}
}

func localBaseline(input BaselineInput) BaselinePlan {
	topics := []BaselineTopic{
		{
			Title:           "Linux production troubleshooting",
			Category:        "operations",
			QuestionType:    models.QuestionTypeScenario,
			InitialQuestion: "A Linux service suddenly has high latency and CPU usage. How would you investigate and stabilize it?",
		},
		{
			Title:           "Kubernetes reliability",
			Category:        "kubernetes",
			QuestionType:    models.QuestionTypePractice,
			InitialQuestion: "How do you debug a Kubernetes deployment where pods are restarting after a new release?",
		},
		{
			Title:           "Kubernetes networking and ingress",
			Category:        "kubernetes-networking",
			QuestionType:    models.QuestionTypeScenario,
			InitialQuestion: "A service works inside the cluster but fails through ingress. How would you troubleshoot it?",
		},
		{
			Title:           "Helm release management",
			Category:        "helm",
			QuestionType:    models.QuestionTypePractice,
			InitialQuestion: "How would you design and operate a Helm chart so releases are repeatable, reviewable, and easy to roll back?",
		},
		{
			Title:           "Terraform state and drift",
			Category:        "terraform",
			QuestionType:    models.QuestionTypePractice,
			InitialQuestion: "How do you manage Terraform state safely and respond when production infrastructure drifts from code?",
		},
		{
			Title:           "Cloud IAM and least privilege",
			Category:        "cloud",
			QuestionType:    models.QuestionTypeScenario,
			InitialQuestion: "A workload needs access to cloud resources across environments. How would you design IAM and reduce permission risk?",
		},
		{
			Title:           "Cloud scaling and load balancing",
			Category:        "cloud",
			QuestionType:    models.QuestionTypeTheory,
			InitialQuestion: "What tradeoffs do you consider when configuring autoscaling and load balancing for a production service?",
		},
		{
			Title:           "CI/CD and rollback strategy",
			Category:        "delivery",
			QuestionType:    models.QuestionTypePractice,
			InitialQuestion: "Describe a safe CI/CD pipeline for a production service and how rollback should work.",
		},
		{
			Title:           "GitOps delivery",
			Category:        "gitops",
			QuestionType:    models.QuestionTypePractice,
			InitialQuestion: "How would you use GitOps with tools like Argo CD or Flux to manage Kubernetes deployments safely?",
		},
		{
			Title:           "Observability and SLOs",
			Category:        "reliability",
			QuestionType:    models.QuestionTypeTheory,
			InitialQuestion: "What signals would you monitor for a user-facing API, and how would you define useful SLOs?",
		},
		{
			Title:           "Monitoring tools",
			Category:        "observability",
			QuestionType:    models.QuestionTypePractice,
			InitialQuestion: "How would you build useful Prometheus and Grafana monitoring for a new production service?",
		},
		{
			Title:           "Logging and distributed tracing",
			Category:        "observability",
			QuestionType:    models.QuestionTypeScenario,
			InitialQuestion: "A request is slow across several microservices. How would you use logs and traces to find the bottleneck?",
		},
		{
			Title:           "Incident response",
			Category:        "incident-management",
			QuestionType:    models.QuestionTypeScenario,
			InitialQuestion: "During an outage, how do you balance mitigation, root cause analysis, and stakeholder communication?",
		},
		{
			Title:           "Infrastructure as code",
			Category:        "platform",
			QuestionType:    models.QuestionTypePractice,
			InitialQuestion: "How would you structure Terraform modules so they are reusable but still safe for production changes?",
		},
		{
			Title:           "Secrets and configuration management",
			Category:        "security",
			QuestionType:    models.QuestionTypeScenario,
			InitialQuestion: "A team accidentally exposed a production secret. What immediate actions and long-term controls would you implement?",
		},
		{
			Title:           "Container supply chain security",
			Category:        "security",
			QuestionType:    models.QuestionTypeTheory,
			InitialQuestion: "What practices help you make container images safer before they reach production?",
		},
		{
			Title:           "Platform engineering",
			Category:        "platform-engineering",
			QuestionType:    models.QuestionTypePractice,
			InitialQuestion: "How would you design an internal developer platform that improves delivery speed without hiding operational risk?",
		},
		{
			Title:           "Database and cache operations",
			Category:        "data-platform",
			QuestionType:    models.QuestionTypeScenario,
			InitialQuestion: "Redis latency and database connections spike after a deployment. How would you investigate and protect the service?",
		},
		{
			Title:           "Cost and capacity planning",
			Category:        "finops",
			QuestionType:    models.QuestionTypePractice,
			InitialQuestion: "How do you approach cloud cost optimization while keeping reliability and delivery speed healthy?",
		},
		{
			Title:           "Linux and network fundamentals",
			Category:        "networking",
			QuestionType:    models.QuestionTypeTheory,
			InitialQuestion: "Which Linux and networking checks would you use first when a service cannot connect to a dependency?",
		},
	}

	if input.Mode == models.SessionModeInterview && strings.TrimSpace(input.JDText+input.CVText) != "" {
		topics[0].Title = "Role-fit production troubleshooting"
		topics[0].InitialQuestion = "Based on this role, tell me how you would approach a production incident from first alert to verified recovery."
	}

	return BaselinePlan{Topics: topics}
}

func localEvaluate(input TurnInput) TurnEvaluation {
	words := strings.Fields(input.Transcript)
	technicalKeywords := []string{"metric", "log", "trace", "slo", "rollback", "mitigate", "hypothesis", "kubernetes", "terraform", "helm", "cloud", "iam", "cicd", "gitops", "prometheus", "grafana", "latency", "error", "deploy", "postgres", "redis", "runbook", "autoscaling", "network"}
	keywordHits := 0
	lower := strings.ToLower(input.Transcript)
	for _, keyword := range technicalKeywords {
		if strings.Contains(lower, keyword) {
			keywordHits++
		}
	}

	technical := clampScore(48 + math.Min(float64(len(words))*0.8, 25) + float64(keywordHits)*3)
	english := clampScore(55 + math.Min(float64(len(words))*0.55, 25))
	if strings.Contains(lower, "first") || strings.Contains(lower, "then") || strings.Contains(lower, "after") {
		english += 5
	}
	english = clampScore(english)

	shouldFollow := technical < 76 || len(words) < 45
	return TurnEvaluation{
		TranscriptSummary: summarizeTranscript(input.Transcript),
		TechnicalScore:    technical,
		EnglishScore:      english,
		Strengths:         []string{"Clear operational intent", "Mentioned practical troubleshooting steps"},
		Improvements:      []string{"Add more specific signals, commands, and validation criteria"},
		EnglishNotes:      []string{"Use a simple structure: context, action, result, tradeoff"},
		TechnicalNotes:    []string{"Name the exact metrics or logs you would inspect and how they affect the next decision"},
		ReferenceAnswer:   referenceAnswer(input.Topic),
		ShouldFollowUp:    shouldFollow,
		FollowUpQuestion:  fmt.Sprintf("What specific signal would tell you that your approach to %s is working?", input.Topic.Title),
		NextQuestionType:  models.QuestionTypeScenario,
	}
}

func localFinalReport(input FinalReportInput) FinalReport {
	technical, english, overall := AggregateScores(input.Turns)
	answers := make([]ReportAnswer, 0, len(input.Turns))
	for _, turn := range input.Turns {
		if turn.AnsweredAt == nil {
			continue
		}
		answers = append(answers, ReportAnswer{
			Question:        turn.QuestionText,
			Transcript:      turn.Transcript,
			ReferenceAnswer: turn.ReferenceAnswer,
			Score:           turn.OverallScore,
		})
	}

	return FinalReport{
		Summary:                 "You showed a practical SRE mindset. To sound stronger in interviews, make answers more structured and tie each action to production impact.",
		OverallScore:            overall,
		TechnicalScore:          technical,
		EnglishScore:            english,
		EnglishImprovementAreas: []string{"Use shorter sentences when explaining incidents", "Practice signposting with first, next, then, and finally"},
		TechnicalSkillGaps:      []string{"Be more specific about metrics, rollback criteria, and validation after mitigation"},
		RecommendedPractice:     []string{"Prepare 3 incident stories using STAR", "Practice Kubernetes debugging aloud", "Review SLO/error budget examples"},
		Answers:                 answers,
	}
}

func normalizeBaseline(plan BaselinePlan) BaselinePlan {
	out := make([]BaselineTopic, 0, len(plan.Topics))
	for _, topic := range plan.Topics {
		topic.Title = trimOrDefault(topic.Title, "SRE operations")
		topic.Category = trimOrDefault(topic.Category, "sre")
		topic.QuestionType = normalizeQuestionType(topic.QuestionType)
		topic.InitialQuestion = trimOrDefault(topic.InitialQuestion, "Walk me through how you would troubleshoot a production reliability issue.")
		out = append(out, topic)
	}
	return BaselinePlan{Topics: out}
}

func normalizeQuestionType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case models.QuestionTypeTheory:
		return models.QuestionTypeTheory
	case models.QuestionTypePractice:
		return models.QuestionTypePractice
	case models.QuestionTypeScenario:
		return models.QuestionTypeScenario
	default:
		return models.QuestionTypeScenario
	}
}

func trimOrDefault(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func summarizeTranscript(value string) string {
	words := strings.Fields(value)
	if len(words) <= 28 {
		return strings.TrimSpace(value)
	}
	return strings.Join(words[:28], " ") + "..."
}

func referenceAnswer(topic models.Topic) string {
	switch topic.Category {
	case "kubernetes":
		return "A concise answer should check rollout history, pod events, readiness/liveness probes, logs, resource pressure, config changes, and then roll back or mitigate while validating service health."
	case "kubernetes-networking":
		return "A strong answer should verify service selectors, endpoints, DNS, network policy, ingress rules, controller logs, TLS configuration, and backend health before changing traffic."
	case "helm":
		return "A good Helm answer covers values separation, versioned charts, linting and template checks, release history, atomic upgrades, rollback criteria, and secrets handling."
	case "terraform":
		return "A strong Terraform answer mentions remote state locking, clear module boundaries, plan review, drift detection, import or reconciliation steps, and cautious production applies."
	case "cloud":
		return "A concise cloud answer should balance least privilege, identity boundaries, network controls, autoscaling signals, health checks, failover, and cost-aware reliability."
	case "delivery", "gitops":
		return "A strong delivery answer includes automated tests, artifact promotion, approvals for risky changes, progressive rollout, fast rollback, deployment visibility, and post-deploy verification."
	case "observability", "reliability":
		return "A good observability answer starts with user-impacting SLIs, golden signals, actionable alerts, dashboards tied to SLOs, and logs or traces that support debugging."
	case "incident-management":
		return "A strong incident answer separates mitigation from root cause analysis, keeps communication clear, uses an incident commander, and verifies recovery with user-impact metrics."
	case "security":
		return "A solid security answer covers immediate containment, rotation, audit, least privilege, scanning, policy enforcement, and a follow-up control that prevents recurrence."
	case "platform-engineering":
		return "A strong platform answer gives developers paved-road workflows with templates, guardrails, observability, self-service, ownership boundaries, and escape hatches for advanced cases."
	case "data-platform":
		return "A concise answer should compare deploy timing, connection pools, query or command latency, saturation, cache hit rate, rollback options, and protection for downstream dependencies."
	case "finops":
		return "A strong answer ties cost changes to usage, rightsizing, autoscaling, reserved capacity, cleanup automation, ownership dashboards, and reliability guardrails."
	case "networking":
		return "A good answer checks DNS, routing, firewall or security groups, listening ports, TLS, packet loss, latency, and service-side logs before changing configuration."
	default:
		return "A strong answer states the symptom, gathers metrics and logs, forms a hypothesis, applies the smallest safe change, and verifies recovery against SLO or customer-impact signals."
	}
}

func trimForPrompt(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n[truncated]"
}

func clampScore(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return math.Round(value*10) / 10
}

func AggregateScores(turns []models.Turn) (technical float64, english float64, overall float64) {
	var count float64
	for _, turn := range turns {
		if turn.AnsweredAt == nil {
			continue
		}
		technical += turn.TechnicalScore
		english += turn.EnglishScore
		count++
	}
	if count == 0 {
		return 0, 0, 0
	}
	technical = clampScore(technical / count)
	english = clampScore(english / count)
	overall = clampScore(technical*0.6 + english*0.4)
	return technical, english, overall
}
