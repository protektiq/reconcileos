package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"reconcileos.dev/api/config"
	"reconcileos.dev/api/db"
	"reconcileos.dev/api/handlers"
	"reconcileos.dev/api/middleware"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	cfg := config.Load()
	clients, err := db.NewSupabaseClients(cfg.SupabaseURL, cfg.SupabaseAnonKey, cfg.SupabaseServiceKey)
	if err != nil {
		panic(err)
	}

	zerolog.TimeFieldFormat = time.RFC3339Nano
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()

	router := gin.New()
	router.Use(requestIDMiddleware())
	router.Use(requestLogger())
	router.Use(recoveryLogger())
	router.Use(corsMiddleware(cfg.AllowedOriginFly))

	router.GET("/health", handlers.Health(cfg.AppVersion))

	authGroup := router.Group("/auth")
	{
		_ = authGroup
	}

	jwtMiddleware, err := middleware.JWTAuthMiddleware(cfg.SupabaseURL, clients)
	if err != nil {
		panic(err)
	}

	apiV1Group := router.Group("/api/v1")
	apiV1Group.Use(jwtMiddleware)
	{
		_ = apiV1Group
	}

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info().Str("addr", server.Addr).Msg("starting HTTP server")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-signalCtx.Done()
	log.Info().Msg("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("graceful shutdown failed")
	}
}

func corsMiddleware(allowedOriginFly string) gin.HandlerFunc {
	origins := []string{"http://localhost:5173"}
	if allowedOriginFly != "" {
		origins = append(origins, allowedOriginFly)
	}

	return cors.New(cors.Config{
		AllowOrigins:     origins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Authorization", "Content-Type", "X-Request-ID"},
		ExposeHeaders:    []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()

		requestID := c.GetHeader("X-Request-ID")
		log.Info().
			Str("request_id", requestID).
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status", c.Writer.Status()).
			Dur("latency", time.Since(started)).
			Str("client_ip", c.ClientIP()).
			Msg("http_request")
	}
}

func recoveryLogger() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		requestID := c.GetHeader("X-Request-ID")
		log.Error().
			Interface("panic", recovered).
			Str("request_id", requestID).
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Msg("panic_recovered")

		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
		})
	})
}

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if requestID == "" {
			requestID = generateRequestID()
		}

		c.Header("X-Request-ID", requestID)
		c.Request.Header.Set("X-Request-ID", requestID)
		c.Next()
	}
}

func generateRequestID() string {
	buffer := make([]byte, 16)
	_, err := rand.Read(buffer)
	if err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}

	return hex.EncodeToString(buffer)
}
