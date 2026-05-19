package database

import (
	"context"
	"fmt"

	"practice-speaking/backend/internal/config"
	"practice-speaking/backend/internal/models"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Handles struct {
	DB    *gorm.DB
	Redis *redis.Client
}

func Connect(ctx context.Context, cfg config.Config) (Handles, error) {
	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{})
	if err != nil {
		return Handles{}, fmt.Errorf("connect postgres: %w", err)
	}

	if err := db.AutoMigrate(&models.Session{}, &models.Topic{}, &models.Turn{}); err != nil {
		return Handles{}, fmt.Errorf("migrate postgres: %w", err)
	}

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return Handles{}, fmt.Errorf("connect redis: %w", err)
	}

	return Handles{DB: db, Redis: redisClient}, nil
}
