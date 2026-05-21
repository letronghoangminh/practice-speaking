import path from "node:path";
import { expect, test } from "@playwright/test";

const technicalAnswer =
  "I would first confirm user impact with latency and error-rate metrics, then inspect logs, CPU saturation, deployment history, and dependency health. I would mitigate with a rollback or scaling change, communicate status, and verify recovery against SLO signals.";

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(window, "speechSynthesis", {
      configurable: true,
      value: {
        cancel: () => undefined,
        speak: () => undefined,
      },
    });
  });
});

test("practice mode supports typed answer, follow-up, scoring, and final report", async ({ page }) => {
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "SRE interview room" })).toBeVisible();
  await expect(page.getByTestId("duration-minutes")).toHaveValue("20");
  await page.getByRole("button", { name: "Practice", exact: true }).click();
  await expect(page.getByTestId("duration-minutes")).toHaveValue("10");
  await page.getByTestId("start-session").click();

  await expect(page.getByText("Live session")).toBeVisible();
  await expect(page.getByTestId("current-question")).toContainText("Linux service");
  await expect(page.getByText("The interviewer voice is AI-generated.")).toBeVisible();

  await page.getByTestId("typed-answer").fill(technicalAnswer);
  await page.getByTestId("submit-text").click();

  await expect(page.getByTestId("last-answer")).toBeVisible();
  await expect(page.getByText("Tech 97")).toBeVisible();
  await expect(page.getByText(/^English \d+$/)).toBeVisible();
  await expect(page.getByTestId("current-question")).toContainText("What specific signal");

  await page.getByTestId("finish-session").click();

  await expect(page.getByText("Session report")).toBeVisible();
  await expect(page.getByTestId("final-report")).toContainText("Final report");
  await expect(page.getByTestId("final-report")).toContainText("Use shorter sentences");
  await expect(page.getByTestId("final-report")).toContainText("Prepare 3 incident stories");

  await page.getByTestId("qa-details-0").click();
  await expect(page.getByTestId("qa-review-modal")).toContainText("Answer review");
  await expect(page.getByTestId("qa-review-modal")).toContainText("Tech 97");
  await expect(page.getByTestId("qa-review-modal")).toContainText("Your answer summary");
  await expect(page.getByTestId("qa-review-modal")).toContainText("Suggested answer summary");
  await expect(page.getByTestId("qa-review-modal")).toContainText("Full transcript");
  await page.getByRole("button", { name: "Close answer review" }).click();
  await expect(page.getByTestId("qa-review-modal")).toBeHidden();
});

test("practice mode can skip an unknown topic without scoring it", async ({ page }) => {
  await page.goto("/");

  await page.getByRole("button", { name: "Practice", exact: true }).click();
  await page.getByTestId("start-session").click();

  await expect(page.getByTestId("current-question")).toContainText("Linux service");
  await page.getByTestId("skip-question").click();

  await expect(page.getByTestId("current-question")).toContainText("Kubernetes deployment");
  await expect(
    page.locator(".topic-row").filter({ hasText: "Linux production troubleshooting" }).getByText("0/3 questions"),
  ).toBeVisible();

  await page.getByTestId("typed-answer").fill(technicalAnswer);
  await page.getByTestId("submit-text").click();

  await expect(page.getByTestId("last-answer")).toBeVisible();
  await page.getByTestId("finish-session").click();

  await expect(page.getByTestId("final-report")).toContainText("Final report");
  await expect(page.getByTestId("qa-details-0")).toBeVisible();
  await expect(page.getByTestId("qa-details-1")).toHaveCount(0);
});

test("interview mode accepts JD and CV files and generates role-fit questions", async ({ page }) => {
  await page.goto("/");

  await page.getByTestId("jd-file").setInputFiles(path.resolve("../samples/devops-jd.md"));
  await page.getByTestId("cv-file").setInputFiles(path.resolve("../samples/cv.md"));
  await expect(page.getByText("devops-jd.md")).toBeVisible();
  await expect(page.getByText("cv.md")).toBeVisible();

  await page.getByTestId("start-session").click();

  await expect(page.getByText("Live session")).toBeVisible();
  await expect(page.getByText("interview mode")).toBeVisible();
  await expect(page.getByTestId("current-question")).toContainText("Based on your CV");
  await expect(page.getByText("CV experience: owned project")).toBeVisible();

  await page.getByTestId("typed-answer").fill(technicalAnswer);
  await page.getByTestId("submit-text").click();
  await expect(page.getByTestId("last-answer")).toBeVisible();

  await page.getByTestId("finish-session").click();
  await expect(page.getByTestId("final-report")).toContainText("Final report");
  await expect(page.getByTestId("final-report")).toContainText("Be more specific");
});
