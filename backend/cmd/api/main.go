package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"practice-speaking/backend/internal/config"
	"practice-speaking/backend/internal/database"
	"practice-speaking/backend/internal/httpapi"
	"practice-speaking/backend/internal/services"
)

func main() {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	handles, err := database.Connect(ctx, cfg)
	if err != nil {
		log.Fatalf("startup failed: %v", err)
	}

	ai := services.NewOpenAIClient(cfg)
	sessionService := services.NewSessionService(handles.DB, handles.Redis, ai)
	server := &http.Server{
		Addr:              ":" + cfg.APIPort,
		Handler:           httpapi.New(sessionService, cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("api listening on :%s", cfg.APIPort)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown failed: %v", err)
	}
}
