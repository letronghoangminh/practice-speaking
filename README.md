# Practice Speaking

A local single-user application for practicing English technical interviews for DevOps and SRE roles.

## Stack

- Backend: Go, Gin, GORM, PostgreSQL, Redis
- Frontend: Next.js, Tailwind CSS, lucide-react
- AI: OpenAI Responses API, JD image text extraction, speech-to-text, and text-to-speech
- Default cost-conscious models: `gpt-5.4-mini`, `gpt-4o-mini` for JD image extraction, `gpt-4o-mini-transcribe`, and `gpt-4o-mini-tts`

## Quick Start

1. Copy environment values:

   ```bash
   cp .env.example .env
   ```

2. Add `OPENAI_API_KEY` in `.env`.

3. Start the stack:

   ```bash
   docker compose up --build
   ```

4. Open [http://localhost:13000](http://localhost:13000).

The backend is available at [http://localhost:18080/api/v1](http://localhost:18080/api/v1).

If `OPENAI_API_KEY` is empty, the API runs with deterministic local fallbacks so the app can still be explored.

Interview uploads support JD files as `.txt`, `.md`, `.pdf`, `.png`, `.jpg`, `.jpeg`, or `.webp`; CV uploads support `.md` and `.pdf`.
Session duration can be set per run from the start form. Defaults are 20 minutes for Interview mode and 10 minutes for Practice mode.
Interview sessions begin with explicit CV-experience questions before moving into JD-aligned technical topics.

## Local Development

Backend:

```bash
cd backend
go test ./...
go run ./cmd/api
```

Frontend:

```bash
cd frontend
npm install
npm run lint
npm run build
npm run dev
```

## E2E Tests

The E2E suite uses a Compose override that disables `OPENAI_API_KEY` during tests, so it does not spend API credits.

```bash
cd frontend
npm run test:e2e
```

This starts an isolated test stack on `http://localhost:13001` and `http://localhost:18081/api/v1`, runs Playwright, then tears down the test containers and volumes.
