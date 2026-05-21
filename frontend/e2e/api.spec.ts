import { expect, test } from "@playwright/test";

const apiURL = process.env.E2E_API_URL ?? "http://localhost:18080/api/v1";
const conciseAnswer =
  "I would check metrics, logs, deployment history, Kubernetes events, error rate, latency, saturation, and rollback if user impact is high.";

test("API health and text session lifecycle work end to end", async ({ request }) => {
  const health = await request.get(`${apiURL}/health`);
  await expect(health).toBeOK();
  await expect(await health.json()).toEqual({ ok: true });

  const created = await request.post(`${apiURL}/sessions`, {
    form: { mode: "practice" },
  });
  await expect(created).toBeOK();
  const createdBody = await created.json();
  const id = createdBody.session.id as number;
  const firstTopicId = createdBody.current_question.topic_id as number;
  const practiceDurationMs =
    new Date(createdBody.session.deadline_at).getTime() - new Date(createdBody.session.started_at).getTime();
  expect(practiceDurationMs).toBe(10 * 60 * 1000);

  for (let index = 0; index < 3; index += 1) {
    const answer = await request.post(`${apiURL}/sessions/${id}/answer-text`, {
      data: { answer: conciseAnswer },
    });
    await expect(answer).toBeOK();
    const body = await answer.json();

    if (index < 2) {
      expect(body.current_question.topic_id).toBe(firstTopicId);
      expect(body.current_question.is_follow_up).toBe(true);
    } else {
      expect(body.current_question.topic_id).not.toBe(firstTopicId);
      expect(body.current_question.is_follow_up).toBe(false);
    }
  }

  const finalized = await request.post(`${apiURL}/sessions/${id}/finalize`);
  await expect(finalized).toBeOK();
  const finalizedBody = await finalized.json();
  expect(finalizedBody.session.status).toBe("completed");
  expect(finalizedBody.current_question).toBeUndefined();

  const report = await request.get(`${apiURL}/sessions/${id}/report`);
  await expect(report).toBeOK();
  const reportBody = await report.json();
  expect(reportBody.summary).toContain("practical SRE mindset");
  expect(reportBody.answers.length).toBeGreaterThan(0);
});

test("API skip question advances topic without scoring the skipped turn", async ({ request }) => {
  const created = await request.post(`${apiURL}/sessions`, {
    form: { mode: "practice" },
  });
  await expect(created).toBeOK();
  const createdBody = await created.json();
  const id = createdBody.session.id as number;
  const firstTopicId = createdBody.current_question.topic_id as number;

  const skipped = await request.post(`${apiURL}/sessions/${id}/skip-question`);
  await expect(skipped).toBeOK();
  const skippedBody = await skipped.json();

  expect(skippedBody.current_question.topic_id).not.toBe(firstTopicId);
  expect(skippedBody.session.topics[0].asked_count).toBe(0);
  expect(skippedBody.session.topics[0].completed).toBe(true);
  expect(skippedBody.session.turns[0].skipped_at).toBeTruthy();
  expect(skippedBody.session.turns[0].answered_at).toBeUndefined();
  expect(skippedBody.session.overall_score).toBe(0);

  const answer = await request.post(`${apiURL}/sessions/${id}/answer-text`, {
    data: { answer: conciseAnswer },
  });
  await expect(answer).toBeOK();
  const answerBody = await answer.json();
  const answeredTurns = answerBody.session.turns.filter((turn: { answered_at?: string }) => turn.answered_at);
  const skippedTurns = answerBody.session.turns.filter((turn: { skipped_at?: string }) => turn.skipped_at);
  expect(answeredTurns).toHaveLength(1);
  expect(skippedTurns).toHaveLength(1);
});

test("API accepts custom session duration", async ({ request }) => {
  const practice = await request.post(`${apiURL}/sessions`, {
    form: { mode: "practice", duration_minutes: "12" },
  });
  await expect(practice).toBeOK();
  const practiceBody = await practice.json();
  const durationMs =
    new Date(practiceBody.session.deadline_at).getTime() - new Date(practiceBody.session.started_at).getTime();
  expect(durationMs).toBe(12 * 60 * 1000);

  const invalid = await request.post(`${apiURL}/sessions`, {
    form: { mode: "practice", duration_minutes: "121" },
  });
  expect(invalid.status()).toBe(400);
  const invalidBody = await invalid.json();
  expect(invalidBody.error.message).toContain("duration_minutes");
});

test("API audio answer endpoint works in deterministic fallback mode", async ({ request }) => {
  const created = await request.post(`${apiURL}/sessions`, {
    form: { mode: "practice" },
  });
  await expect(created).toBeOK();
  const id = (await created.json()).session.id as number;

  const audioAnswer = await request.post(`${apiURL}/sessions/${id}/answer-audio`, {
    multipart: {
      audio: {
        name: "answer.webm",
        mimeType: "audio/webm",
        buffer: Buffer.from("fake-webm-audio-for-fallback-transcription"),
      },
    },
  });
  await expect(audioAnswer).toBeOK();
  const body = await audioAnswer.json();
  expect(body.session.turns[0].transcript).toContain("Local fallback transcript");
  expect(body.session.turns[0].overall_score).toBeGreaterThan(0);
});

test("API rejects unsupported interview file types clearly", async ({ request }) => {
  const response = await request.post(`${apiURL}/sessions`, {
    multipart: {
      mode: "interview",
      jd_file: {
        name: "job.exe",
        mimeType: "application/octet-stream",
        buffer: Buffer.from("not a supported document"),
      },
      cv_text: "SRE candidate with Kubernetes and Terraform experience.",
    },
  });

  expect(response.status()).toBe(400);
  const body = await response.json();
  expect(body.error.message).toContain("unsupported file type");
});

test("API accepts JD image upload and rejects CV txt upload", async ({ request }) => {
  const imageJD = await request.post(`${apiURL}/sessions`, {
    multipart: {
      mode: "interview",
      jd_file: {
        name: "jd-screenshot.png",
        mimeType: "image/png",
        buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
      },
      cv_text: "SRE candidate with Kubernetes, Terraform, and CI/CD experience.",
    },
  });
  await expect(imageJD).toBeOK();
  const imageBody = await imageJD.json();
  expect(imageBody.current_question.question_text).toContain("Based on your CV");
  expect(imageBody.session.topics[0].category).toBe("cv-experience");
  expect(imageBody.session.jd_text).toContain("Local fallback extracted JD image text");

  const txtCV = await request.post(`${apiURL}/sessions`, {
    multipart: {
      mode: "interview",
      jd_text: "DevOps role with Kubernetes and Terraform.",
      cv_file: {
        name: "cv.txt",
        mimeType: "text/plain",
        buffer: Buffer.from("CV text should not be accepted as an upload."),
      },
    },
  });
  expect(txtCV.status()).toBe(400);
  const txtCVBody = await txtCV.json();
  expect(txtCVBody.error.message).toContain("CV file");
  expect(txtCVBody.error.message).toContain(".md or .pdf");
});
