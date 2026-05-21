"use client";

import {
  AlertCircle,
  BarChart3,
  CheckCircle2,
  CircleSlash,
  Clock,
  FileText,
  History,
  Mic,
  Pause,
  Play,
  Send,
  Square,
  Upload,
  X,
} from "lucide-react";
import { FormEvent, forwardRef, useEffect, useMemo, useRef, useState } from "react";
import {
  createSession,
  finalizeSession,
  getReport,
  getSession,
  listSessions,
  Mode,
  FinalReport,
  Session,
  SessionEnvelope,
  skipQuestion,
  submitAudioAnswer,
  submitTextAnswer,
  Turn,
} from "@/lib/api";

const DEFAULT_INTERVIEW_MINUTES = 20;
const DEFAULT_PRACTICE_MINUTES = 10;
const MIN_SESSION_MINUTES = 1;
const MAX_SESSION_MINUTES = 120;

export default function Home() {
  const [mode, setMode] = useState<Mode>("interview");
  const [interviewMinutes, setInterviewMinutes] = useState(DEFAULT_INTERVIEW_MINUTES);
  const [practiceMinutes, setPracticeMinutes] = useState(DEFAULT_PRACTICE_MINUTES);
  const [jdText, setJdText] = useState("");
  const [cvText, setCvText] = useState("");
  const [jdFile, setJdFile] = useState<File | null>(null);
  const [cvFile, setCvFile] = useState<File | null>(null);
  const [session, setSession] = useState<Session | null>(null);
  const [currentQuestion, setCurrentQuestion] = useState<Turn | undefined>();
  const [report, setReport] = useState<FinalReport | null>(null);
  const [history, setHistory] = useState<Session[]>([]);
  const [typedAnswer, setTypedAnswer] = useState("");
  const [busy, setBusy] = useState(false);
  const [isRecording, setIsRecording] = useState(false);
  const [error, setError] = useState("");
  const [now, setNow] = useState(Date.now());

  const recorderRef = useRef<MediaRecorder | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const chunksRef = useRef<Blob[]>([]);
  const reportRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    void refreshHistory();
  }, []);

  useEffect(() => {
    const interval = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(interval);
  }, []);

  const remainingMs = useMemo(() => {
    if (!session || session.status !== "active") return 0;
    return Math.max(0, new Date(session.deadline_at).getTime() - now);
  }, [session, now]);

  const lastAnswered = useMemo(() => {
    return session?.turns?.filter((turn) => turn.answered_at).at(-1);
  }, [session]);

  const selectedDurationMinutes = mode === "interview" ? interviewMinutes : practiceMinutes;

  useEffect(() => {
    if (session?.status === "completed" && report) {
      reportRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    }
  }, [session?.status, report]);

  async function refreshHistory() {
    try {
      setHistory(await listSessions());
    } catch {
      setHistory([]);
    }
  }

  async function handleStart(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setReport(null);

    try {
      const form = new FormData();
      form.append("mode", mode);
      form.append("duration_minutes", String(clampDuration(selectedDurationMinutes)));
      if (jdFile) form.append("jd_file", jdFile);
      if (cvFile) form.append("cv_file", cvFile);
      if (jdText.trim()) form.append("jd_text", jdText.trim());
      if (cvText.trim()) form.append("cv_text", cvText.trim());
      const envelope = await createSession(form);
      await acceptEnvelope(envelope, true);
    } catch (err) {
      setError(messageFromError(err));
    } finally {
      setBusy(false);
    }
  }

  async function acceptEnvelope(envelope: SessionEnvelope, playAudio: boolean) {
    setSession(envelope.session);
    setCurrentQuestion(envelope.current_question);
    setTypedAnswer("");
    setReport(null);
    if (envelope.session.status === "completed") {
      const final = await getReport(envelope.session.id);
      setReport(final);
    }
    if (playAudio) {
      playQuestion(envelope);
    }
    await refreshHistory();
  }

  function playQuestion(envelope: SessionEnvelope) {
    const text = envelope.current_question?.question_text;
    if (envelope.audio_base64 && envelope.audio_mime) {
      const audio = new Audio(`data:${envelope.audio_mime};base64,${envelope.audio_base64}`);
      void audio.play();
      return;
    }
    if (text && "speechSynthesis" in window) {
      window.speechSynthesis.cancel();
      const utterance = new SpeechSynthesisUtterance(text);
      utterance.lang = "en-US";
      utterance.rate = 0.96;
      window.speechSynthesis.speak(utterance);
    }
  }

  async function handleSubmitText() {
    if (!session || !typedAnswer.trim()) return;
    setBusy(true);
    setError("");
    try {
      const envelope = await submitTextAnswer(session.id, typedAnswer.trim());
      await acceptEnvelope(envelope, true);
    } catch (err) {
      setError(messageFromError(err));
    } finally {
      setBusy(false);
    }
  }

  async function handleSkipQuestion() {
    if (!session || !currentQuestion) return;
    setBusy(true);
    setError("");
    try {
      if ("speechSynthesis" in window) {
        window.speechSynthesis.cancel();
      }
      const envelope = await skipQuestion(session.id);
      await acceptEnvelope(envelope, true);
    } catch (err) {
      setError(messageFromError(err));
    } finally {
      setBusy(false);
    }
  }

  async function startRecording() {
    if (!session || busy) return;
    setError("");
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      streamRef.current = stream;
      chunksRef.current = [];
      const mimeType = MediaRecorder.isTypeSupported("audio/webm") ? "audio/webm" : "";
      const recorder = new MediaRecorder(stream, mimeType ? { mimeType } : undefined);
      recorderRef.current = recorder;
      recorder.ondataavailable = (event) => {
        if (event.data.size > 0) chunksRef.current.push(event.data);
      };
      recorder.onstop = () => {
        const blob = new Blob(chunksRef.current, { type: mimeType || "audio/webm" });
        streamRef.current?.getTracks().forEach((track) => track.stop());
        streamRef.current = null;
        void submitRecordedAnswer(blob);
      };
      recorder.start();
      setIsRecording(true);
    } catch (err) {
      setError(messageFromError(err));
    }
  }

  function stopRecording() {
    if (recorderRef.current && recorderRef.current.state !== "inactive") {
      recorderRef.current.stop();
    }
    setIsRecording(false);
  }

  async function submitRecordedAnswer(blob: Blob) {
    if (!session) return;
    setBusy(true);
    setError("");
    try {
      const envelope = await submitAudioAnswer(session.id, blob);
      await acceptEnvelope(envelope, true);
    } catch (err) {
      setError(messageFromError(err));
    } finally {
      setBusy(false);
    }
  }

  async function handleFinalize() {
    if (!session) return;
    setBusy(true);
    setError("");
    try {
      const envelope = await finalizeSession(session.id);
      await acceptEnvelope(envelope, false);
    } catch (err) {
      setError(messageFromError(err));
    } finally {
      setBusy(false);
    }
  }

  async function openSession(id: number) {
    setBusy(true);
    setError("");
    try {
      const envelope = await getSession(id);
      setSession(envelope.session);
      setCurrentQuestion(envelope.current_question);
      setReport(null);
      setReport(envelope.session.status === "completed" ? await getReport(id) : null);
    } catch (err) {
      setError(messageFromError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="min-h-screen bg-[linear-gradient(180deg,#f7faf8_0%,#eaf2f0_100%)] text-ink">
      <div className="mx-auto flex max-w-7xl flex-col gap-6 px-5 py-6 lg:grid lg:grid-cols-[380px_1fr]">
        <aside className="space-y-5">
          <section className="surface">
            <div className="mb-5 flex items-center justify-between">
              <div>
                <p className="label">Practice Speaking</p>
                <h1 className="text-2xl font-semibold tracking-normal">SRE interview room</h1>
              </div>
              <BarChart3 className="h-6 w-6 text-ocean" aria-hidden="true" />
            </div>

            <form onSubmit={handleStart} className="space-y-4">
              <div className="segmented">
                <button
                  type="button"
                  className={mode === "interview" ? "segment-active" : "segment"}
                  onClick={() => setMode("interview")}
                >
                  <FileText className="h-4 w-4" />
                  Interview
                </button>
                <button
                  type="button"
                  className={mode === "practice" ? "segment-active" : "segment"}
                  onClick={() => setMode("practice")}
                >
                  <Play className="h-4 w-4" />
                  Practice
                </button>
              </div>

              <DurationInput
                value={selectedDurationMinutes}
                onChange={(value) => {
                  if (mode === "interview") {
                    setInterviewMinutes(value);
                  } else {
                    setPracticeMinutes(value);
                  }
                }}
              />

              {mode === "interview" && (
                <div className="space-y-3">
                  <FileInput
                    label="Job description"
                    file={jdFile}
                    onChange={setJdFile}
                    testId="jd-file"
                    accept=".txt,.md,.pdf,.png,.jpg,.jpeg,.webp,image/png,image/jpeg,image/webp"
                  />
                  <textarea
                    className="field min-h-24"
                    value={jdText}
                    onChange={(event) => setJdText(event.target.value)}
                    placeholder="Paste JD text"
                  />
                  <FileInput
                    label="Your CV"
                    file={cvFile}
                    onChange={setCvFile}
                    testId="cv-file"
                    accept=".md,.pdf"
                  />
                  <textarea
                    className="field min-h-24"
                    value={cvText}
                    onChange={(event) => setCvText(event.target.value)}
                    placeholder="Paste CV text"
                  />
                </div>
              )}

              <button type="submit" className="primary w-full" disabled={busy} data-testid="start-session">
                <Play className="h-4 w-4" />
                {busy ? "Starting..." : mode === "interview" ? "Start interview" : "Start practice"}
              </button>
            </form>
          </section>

          <section className="surface">
            <div className="mb-4 flex items-center gap-2">
              <History className="h-5 w-5 text-steel" aria-hidden="true" />
              <h2 className="text-lg font-semibold">History</h2>
            </div>
            <div className="space-y-2">
              {history.length === 0 && <p className="muted">No sessions yet.</p>}
              {history.map((item) => (
                <button key={item.id} className="history-row" onClick={() => openSession(item.id)}>
                  <span className="capitalize">{item.mode}</span>
                  <span className={item.status === "completed" ? "status-done" : "status-active"}>
                    {item.status}
                  </span>
                  <span>{Math.round(item.overall_score || 0)}</span>
                </button>
              ))}
            </div>
          </section>
        </aside>

        <section className="workspace">
          {error && (
            <div className="error">
              <AlertCircle className="h-5 w-5" aria-hidden="true" />
              {error}
            </div>
          )}

          {!session && <EmptyState />}

          {session && (
            <div className="space-y-5">
              <div className="session-bar">
                <div>
                  <p className="label capitalize">{session.mode} mode</p>
                  <h2 className="text-2xl font-semibold">
                    {session.status === "active" ? "Live session" : "Session report"}
                  </h2>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  {session.status === "active" && (
                    <span className="timer">
                      <Clock className="h-4 w-4" />
                      {formatDuration(remainingMs)}
                    </span>
                  )}
                  <span className={session.status === "completed" ? "pill-done" : "pill-active"}>
                    {session.status}
                  </span>
                </div>
              </div>

              {session.status === "active" && (
                <section className="question-panel">
                  <div className="flex items-start justify-between gap-4">
                    <div>
                      <p className="label">{currentTopicTitle(session, currentQuestion)}</p>
                      <h3 className="mt-2 text-3xl font-semibold leading-tight" data-testid="current-question">
                        {currentQuestion?.question_text}
                      </h3>
                    </div>
                    <button
                      className="icon-button"
                      onClick={() => playQuestion({ session, current_question: currentQuestion })}
                      aria-label="Play question"
                      title="Play question"
                    >
                      <Play className="h-5 w-5" />
                    </button>
                  </div>

                  <div className="mt-6 flex flex-wrap gap-3">
                    {!isRecording ? (
                      <button className="record" onClick={startRecording} disabled={busy}>
                        <Mic className="h-5 w-5" />
                        Record answer
                      </button>
                    ) : (
                      <button className="stop" onClick={stopRecording}>
                        <Square className="h-5 w-5" />
                        Stop recording
                      </button>
                    )}
                    <button className="secondary" onClick={handleFinalize} disabled={busy} data-testid="finish-session">
                      <Pause className="h-4 w-4" />
                      Finish
                    </button>
                    <button
                      className="secondary"
                      onClick={handleSkipQuestion}
                      disabled={busy || isRecording}
                      data-testid="skip-question"
                    >
                      <CircleSlash className="h-4 w-4" />
                      I don&apos;t know about this topic
                    </button>
                  </div>

                  <div className="mt-5">
                    <textarea
                      className="field min-h-28"
                      data-testid="typed-answer"
                      value={typedAnswer}
                      onChange={(event) => setTypedAnswer(event.target.value)}
                      placeholder="Typed fallback answer"
                    />
                    <button
                      className="secondary mt-3"
                      onClick={handleSubmitText}
                      disabled={busy || !typedAnswer.trim()}
                      data-testid="submit-text"
                    >
                      <Send className="h-4 w-4" />
                      Submit text
                    </button>
                  </div>

                  <p className="mt-5 text-xs text-steel">The interviewer voice is AI-generated.</p>
                </section>
              )}

              {session.status === "completed" && (
                report ? (
                  <ReportView report={report} turns={session.turns ?? []} ref={reportRef} />
                ) : (
                  <section className="report" data-testid="final-report-loading">
                    <div className="mb-3 flex items-center gap-2">
                      <Clock className="h-5 w-5 text-ocean" aria-hidden="true" />
                      <h3 className="text-2xl font-semibold">Preparing final report</h3>
                    </div>
                    <p className="text-steel">The interview summary and recommendations are loading.</p>
                  </section>
                )
              )}

              {lastAnswered && session.status === "active" && <TurnFeedback turn={lastAnswered} />}

              <TopicProgress session={session} />
            </div>
          )}
        </section>
      </div>
    </main>
  );
}

function FileInput({
  label,
  file,
  onChange,
  testId,
  accept,
}: {
  label: string;
  file: File | null;
  onChange: (file: File | null) => void;
  testId: string;
  accept: string;
}) {
  return (
    <label className="file-input">
      <Upload className="h-4 w-4 text-ocean" aria-hidden="true" />
      <span className="min-w-0 flex-1 truncate">{file ? file.name : label}</span>
      <input
        className="sr-only"
        type="file"
        data-testid={testId}
        accept={accept}
        onChange={(event) => onChange(event.target.files?.[0] ?? null)}
      />
    </label>
  );
}

function DurationInput({ value, onChange }: { value: number; onChange: (value: number) => void }) {
  return (
    <label className="block space-y-2">
      <span className="label">Duration</span>
      <span className="flex items-center gap-2 rounded-md border border-black/10 bg-white px-3 py-2">
        <Clock className="h-4 w-4 text-ocean" aria-hidden="true" />
        <input
          className="w-20 bg-transparent text-sm font-semibold text-ink outline-none"
          data-testid="duration-minutes"
          type="number"
          min={MIN_SESSION_MINUTES}
          max={MAX_SESSION_MINUTES}
          step={1}
          value={value}
          onChange={(event) => onChange(clampDuration(Number(event.target.value)))}
        />
        <span className="text-sm font-medium text-steel">minutes</span>
      </span>
    </label>
  );
}

function EmptyState() {
  return (
    <div className="empty">
      <CheckCircle2 className="h-10 w-10 text-moss" aria-hidden="true" />
      <h2 className="text-3xl font-semibold">Ready when you are.</h2>
      <p className="max-w-xl text-steel">
        Start an interview from a JD and CV, or run a focused SRE practice session with randomized production questions.
      </p>
    </div>
  );
}

function TurnFeedback({ turn }: { turn: Turn }) {
  return (
    <section className="surface">
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h3 className="text-xl font-semibold" data-testid="last-answer">
          Last answer
        </h3>
        <Score label="Tech" value={turn.technical_score} />
        <Score label="English" value={turn.english_score} />
        <Score label="Overall" value={turn.overall_score} />
      </div>
      <p className="mb-3 text-steel">{turn.transcript_summary || turn.transcript}</p>
      <div className="grid gap-3 md:grid-cols-2">
        <FeedbackList title="Strengths" values={turn.feedback?.strengths} />
        <FeedbackList title="Improve" values={turn.feedback?.improvements} />
        <FeedbackList title="English" values={turn.feedback?.english_notes} />
        <FeedbackList title="Technical" values={turn.feedback?.technical_notes} />
      </div>
    </section>
  );
}

function TopicProgress({ session }: { session: Session }) {
  return (
    <section className="surface">
      <h3 className="mb-4 text-xl font-semibold">Topics</h3>
      <div className="grid gap-2 md:grid-cols-2">
        {session.topics?.map((topic) => (
          <div key={topic.id} className="topic-row">
            <div className="min-w-0">
              <p className="truncate font-medium">{topic.title}</p>
              <p className="text-sm text-steel">
                {topic.asked_count}/{topic.max_questions} questions
              </p>
            </div>
            {topic.completed ? <CheckCircle2 className="h-5 w-5 text-moss" /> : <Clock className="h-5 w-5 text-ocean" />}
          </div>
        ))}
      </div>
    </section>
  );
}

type QAReview = {
  id: string;
  question: string;
  transcript: string;
  transcriptSummary: string;
  referenceAnswer: string;
  technicalScore: number;
  englishScore: number;
  overallScore: number;
  feedback?: Turn["feedback"];
};

const ReportView = forwardRef<HTMLElement, { report: FinalReport; turns: Turn[] }>(function ReportView(
  { report, turns },
  ref,
) {
  const [selectedReviewId, setSelectedReviewId] = useState<string | null>(null);
  const reviews = buildQAReviews(report, turns);
  const selectedReview = reviews.find((review) => review.id === selectedReviewId);

  return (
    <section className="report" data-testid="final-report" ref={ref}>
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <h3 className="text-2xl font-semibold">Final report</h3>
        <Score label="Overall" value={report.overall_score} />
        <Score label="Tech" value={report.technical_score} />
        <Score label="English" value={report.english_score} />
      </div>
      <p className="mb-5 text-lg text-steel">{report.summary}</p>

      <div className="grid gap-4 md:grid-cols-3">
        <FeedbackList title="English" values={report.english_improvement_areas} />
        <FeedbackList title="Technical" values={report.technical_skill_gaps} />
        <FeedbackList title="Practice" values={report.recommended_practice} />
      </div>

      <div className="mt-6 space-y-3">
        {reviews.map((review, index) => (
          <div key={review.id} className="answer-detail">
            <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
              <div className="min-w-0">
                <p className="label">Question {index + 1}</p>
                <p className="mt-1 font-semibold">{review.question}</p>
              </div>
              <button
                className="secondary shrink-0"
                onClick={() => setSelectedReviewId(review.id)}
                data-testid={`qa-details-${index}`}
              >
                <FileText className="h-4 w-4" />
                View details
              </button>
            </div>
          </div>
        ))}
      </div>

      {selectedReview && <QAReviewModal review={selectedReview} onClose={() => setSelectedReviewId(null)} />}
    </section>
  );
});

function QAReviewModal({ review, onClose }: { review: QAReview; onClose: () => void }) {
  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <section
        className="qa-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="qa-review-title"
        data-testid="qa-review-modal"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="label">Answer review</p>
            <h3 id="qa-review-title" className="mt-2 text-2xl font-semibold leading-tight">
              {review.question}
            </h3>
          </div>
          <button className="icon-button" onClick={onClose} aria-label="Close answer review" title="Close">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="mt-5 flex flex-wrap gap-2">
          <Score label="Tech" value={review.technicalScore} />
          <Score label="English" value={review.englishScore} />
          <Score label="Overall" value={review.overallScore} />
        </div>

        <div className="mt-5 grid gap-4 lg:grid-cols-2">
          <section className="feedback-list">
            <p className="mb-2 font-semibold">Your answer summary</p>
            <p className="text-sm text-steel">{review.transcriptSummary || summarizeText(review.transcript)}</p>
          </section>
          <section className="feedback-list">
            <p className="mb-2 font-semibold">Suggested answer summary</p>
            <p className="text-sm text-steel">{summarizeText(review.referenceAnswer)}</p>
          </section>
        </div>

        <div className="mt-4 grid gap-3 md:grid-cols-2">
          <FeedbackList title="Strengths" values={review.feedback?.strengths} />
          <FeedbackList title="Improve" values={review.feedback?.improvements} />
          <FeedbackList title="English" values={review.feedback?.english_notes} />
          <FeedbackList title="Technical" values={review.feedback?.technical_notes} />
        </div>

        <details className="answer-detail mt-4">
          <summary>Full transcript</summary>
          <p className="mt-3 text-sm text-steel">{review.transcript || "No transcript captured."}</p>
        </details>
      </section>
    </div>
  );
}

function buildQAReviews(report: FinalReport, turns: Turn[]): QAReview[] {
  const answeredTurns = turns.filter((turn) => turn.answered_at);
  if (answeredTurns.length > 0) {
    return answeredTurns.map((turn) => ({
      id: `turn-${turn.id}`,
      question: turn.question_text,
      transcript: turn.transcript ?? "",
      transcriptSummary: turn.transcript_summary ?? "",
      referenceAnswer: turn.reference_answer ?? "",
      technicalScore: turn.technical_score,
      englishScore: turn.english_score,
      overallScore: turn.overall_score,
      feedback: turn.feedback,
    }));
  }

  return (report.answers ?? []).map((answer, index) => ({
    id: `report-answer-${index}`,
    question: answer.question,
    transcript: answer.transcript,
    transcriptSummary: summarizeText(answer.transcript),
    referenceAnswer: answer.reference_answer,
    technicalScore: answer.score,
    englishScore: answer.score,
    overallScore: answer.score,
  }));
}

function FeedbackList({ title, values }: { title: string; values?: string[] }) {
  const items = values?.filter(Boolean) ?? [];
  return (
    <div className="feedback-list">
      <p className="mb-2 font-semibold">{title}</p>
      {items.length === 0 ? (
        <p className="text-sm text-steel">No notes yet.</p>
      ) : (
        <ul className="space-y-2 text-sm text-steel">
          {items.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Score({ label, value }: { label: string; value: number }) {
  return (
    <span className="score">
      {label} {Math.round(value || 0)}
    </span>
  );
}

function currentTopicTitle(session: Session, turn?: Turn) {
  const topic = session.topics?.find((item) => item.id === turn?.topic_id);
  return topic?.title ?? "Current question";
}

function formatDuration(ms: number) {
  const totalSeconds = Math.ceil(ms / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${seconds.toString().padStart(2, "0")}`;
}

function summarizeText(value: string) {
  const words = value.trim().split(/\s+/).filter(Boolean);
  if (words.length <= 34) {
    return value.trim() || "No summary available.";
  }
  return `${words.slice(0, 34).join(" ")}...`;
}

function clampDuration(value: number) {
  if (!Number.isFinite(value)) return MIN_SESSION_MINUTES;
  return Math.min(MAX_SESSION_MINUTES, Math.max(MIN_SESSION_MINUTES, Math.round(value)));
}

function messageFromError(err: unknown) {
  return err instanceof Error ? err.message : "Something went wrong";
}
