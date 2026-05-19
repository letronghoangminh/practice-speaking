package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	APIPort          string
	DatabaseURL      string
	RedisAddr        string
	AllowedOrigins   []string
	OpenAIAPIKey     string
	OpenAIBaseURL    string
	OpenAITextModel  string
	OpenAISTTModel   string
	OpenAITTSModel   string
	OpenAITTSVoice   string
	OpenAITimeoutSec string
}

func Load() Config {
	_ = godotenv.Load("../.env", ".env")

	return Config{
		APIPort:          env("API_PORT", "8088"),
		DatabaseURL:      env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/practice_speaking?sslmode=disable"),
		RedisAddr:        env("REDIS_ADDR", "localhost:6379"),
		AllowedOrigins:   splitCSV(env("ALLOWED_ORIGINS", "http://localhost:3000")),
		OpenAIAPIKey:     os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL:    env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAITextModel:  env("OPENAI_TEXT_MODEL", "gpt-5.4-mini"),
		OpenAISTTModel:   env("OPENAI_STT_MODEL", "gpt-4o-mini-transcribe"),
		OpenAITTSModel:   env("OPENAI_TTS_MODEL", "gpt-4o-mini-tts"),
		OpenAITTSVoice:   env("OPENAI_TTS_VOICE", "marin"),
		OpenAITimeoutSec: env("OPENAI_TIMEOUT_SECONDS", "90"),
	}
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
