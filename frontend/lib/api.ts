export type Mode = "interview" | "practice";
export type SessionStatus = "active" | "completed";

export type Topic = {
  id: number;
  title: string;
  category: string;
  opening_question?: string;
  order_index: number;
  asked_count: number;
  max_questions: number;
  completed: boolean;
};

export type Turn = {
  id: number;
  topic_id: number;
  question_text: string;
  question_type: "theory" | "practice" | "scenario";
  transcript?: string;
  transcript_summary?: string;
  technical_score: number;
  english_score: number;
  overall_score: number;
  feedback?: {
    strengths?: string[];
    improvements?: string[];
    english_notes?: string[];
    technical_notes?: string[];
  };
  reference_answer?: string;
  is_follow_up: boolean;
  answered_at?: string;
  skipped_at?: string;
  skip_reason?: string;
};

export type Session = {
  id: number;
  mode: Mode;
  status: SessionStatus;
  started_at: string;
  deadline_at: string;
  completed_at?: string;
  current_topic_id?: number;
  overall_score: number;
  technical_score: number;
  english_score: number;
  topics?: Topic[];
  turns?: Turn[];
};

export type SessionEnvelope = {
  session: Session;
  current_question?: Turn;
  audio_base64?: string;
  audio_mime?: string;
};

export type FinalReport = {
  summary: string;
  overall_score: number;
  technical_score: number;
  english_score: number;
  english_improvement_areas?: string[];
  technical_skill_gaps?: string[];
  recommended_practice?: string[];
  answers?: Array<{
    question: string;
    transcript: string;
    reference_answer: string;
    score: number;
  }>;
};

const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:18080/api/v1";

export async function createSession(form: FormData): Promise<SessionEnvelope> {
  const response = await fetch(`${API_URL}/sessions`, {
    method: "POST",
    body: form,
  });
  return parseResponse(response);
}

export async function listSessions(): Promise<Session[]> {
  const response = await fetch(`${API_URL}/sessions`, { cache: "no-store" });
  const data = await parseResponse<{ sessions: Session[] }>(response);
  return data.sessions;
}

export async function getSession(id: number): Promise<SessionEnvelope> {
  const response = await fetch(`${API_URL}/sessions/${id}`, { cache: "no-store" });
  return parseResponse(response);
}

export async function submitTextAnswer(id: number, answer: string): Promise<SessionEnvelope> {
  const response = await fetch(`${API_URL}/sessions/${id}/answer-text`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ answer }),
  });
  return parseResponse(response);
}

export async function submitAudioAnswer(id: number, blob: Blob): Promise<SessionEnvelope> {
  const form = new FormData();
  form.append("audio", blob, "answer.webm");
  const response = await fetch(`${API_URL}/sessions/${id}/answer-audio`, {
    method: "POST",
    body: form,
  });
  return parseResponse(response);
}

export async function skipQuestion(id: number): Promise<SessionEnvelope> {
  const response = await fetch(`${API_URL}/sessions/${id}/skip-question`, {
    method: "POST",
  });
  return parseResponse(response);
}

export async function finalizeSession(id: number): Promise<SessionEnvelope> {
  const response = await fetch(`${API_URL}/sessions/${id}/finalize`, {
    method: "POST",
  });
  return parseResponse(response);
}

export async function getReport(id: number): Promise<FinalReport> {
  const response = await fetch(`${API_URL}/sessions/${id}/report`, { cache: "no-store" });
  return parseResponse(response);
}

async function parseResponse<T>(response: Response): Promise<T> {
  const contentType = response.headers.get("content-type") ?? "";
  const data = contentType.includes("application/json") ? await response.json() : await response.text();
  if (!response.ok) {
    const message = typeof data === "string" ? data : data?.error?.message ?? "Request failed";
    throw new Error(message);
  }
  return data as T;
}
