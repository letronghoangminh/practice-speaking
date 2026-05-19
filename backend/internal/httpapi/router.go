package httpapi

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"practice-speaking/backend/internal/config"
	"practice-speaking/backend/internal/services"

	"github.com/gin-gonic/gin"
)

type Router struct {
	service *services.SessionService
	cfg     config.Config
}

func New(service *services.SessionService, cfg config.Config) http.Handler {
	router := &Router{service: service, cfg: cfg}
	engine := gin.New()
	engine.Use(gin.Logger(), gin.Recovery(), router.cors())

	api := engine.Group("/api/v1")
	api.GET("/health", router.health)
	api.POST("/sessions", router.createSession)
	api.GET("/sessions", router.listSessions)
	api.GET("/sessions/:id", router.getSession)
	api.GET("/sessions/:id/report", router.getReport)
	api.POST("/sessions/:id/answer-text", router.answerText)
	api.POST("/sessions/:id/answer-audio", router.answerAudio)
	api.POST("/sessions/:id/skip-question", router.skipQuestion)
	api.POST("/sessions/:id/finalize", router.finalizeSession)

	return engine
}

func (r *Router) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (r *Router) createSession(c *gin.Context) {
	var jdFile, cvFile = optionalFile(c, "jd_file"), optionalFile(c, "cv_file")
	envelope, err := r.service.CreateSession(c.Request.Context(), services.CreateSessionInput{
		Mode:   c.PostForm("mode"),
		JDText: c.PostForm("jd_text"),
		CVText: c.PostForm("cv_text"),
		JDFile: jdFile,
		CVFile: cvFile,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, envelope)
}

func (r *Router) listSessions(c *gin.Context) {
	sessions, err := r.service.ListSessions(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

func (r *Router) getSession(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	envelope, err := r.service.GetSession(c.Request.Context(), id, services.AudioResult{})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, envelope)
}

func (r *Router) getReport(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	report, err := r.service.GetReport(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

func (r *Router) answerText(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var payload struct {
		Answer string `json:"answer"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		writeError(c, services.AppError{Kind: services.ErrorKindValidation, Message: "answer JSON is invalid"})
		return
	}
	envelope, err := r.service.SubmitTextAnswer(c.Request.Context(), id, payload.Answer)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, envelope)
}

func (r *Router) answerAudio(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	file, err := c.FormFile("audio")
	if err != nil {
		writeError(c, services.AppError{Kind: services.ErrorKindValidation, Message: "multipart field 'audio' is required"})
		return
	}
	opened, err := file.Open()
	if err != nil {
		writeError(c, err)
		return
	}
	defer opened.Close()

	audio, err := io.ReadAll(io.LimitReader(opened, 25<<20+1))
	if err != nil {
		writeError(c, err)
		return
	}
	if len(audio) > 25<<20 {
		writeError(c, services.AppError{Kind: services.ErrorKindValidation, Message: "audio is larger than the 25 MB limit"})
		return
	}

	envelope, err := r.service.SubmitAudioAnswer(c.Request.Context(), id, file.Filename, file.Header.Get("Content-Type"), audio)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, envelope)
}

func (r *Router) skipQuestion(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	envelope, err := r.service.SkipCurrentQuestion(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, envelope)
}

func (r *Router) finalizeSession(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	envelope, err := r.service.FinalizeSession(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, envelope)
}

func optionalFile(c *gin.Context, name string) *multipart.FileHeader {
	file, err := c.FormFile(name)
	if err != nil {
		return nil
	}
	return file
}

func parseID(c *gin.Context) (uint, bool) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id64 == 0 {
		writeError(c, services.AppError{Kind: services.ErrorKindValidation, Message: "session id is invalid"})
		return 0, false
	}
	return uint(id64), true
}

func writeError(c *gin.Context, err error) {
	var appErr services.AppError
	if errors.As(err, &appErr) {
		status := http.StatusBadRequest
		switch appErr.Kind {
		case services.ErrorKindNotFound:
			status = http.StatusNotFound
		case services.ErrorKindConflict:
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": appErr})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"kind": "internal", "message": err.Error()}})
}

func (r *Router) cors() gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, origin := range r.cfg.AllowedOrigins {
		allowed[strings.TrimSpace(origin)] = true
	}

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if allowed[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
