package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/contentpipeline"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/internal/speech"
	"github.com/oppositenum/ai-learning-tutor/server/internal/trial"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutoraudit"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
)

func main() {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	handler := newHandler()
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		pool, err := pgxpool.New(context.Background(), databaseURL)
		if err != nil {
			log.Fatalf("create database pool: %v", err)
		}
		defer pool.Close()
		if err := pool.Ping(context.Background()); err != nil {
			log.Fatalf("connect database: %v", err)
		}
		handler = newDatabaseHandler(pool)
	} else {
		log.Print("DATABASE_URL is not set; only the health endpoint is enabled")
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("api listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func newHandler() http.Handler {
	return api.NewRouter(api.Dependencies{})
}

func newDatabaseHandler(pool *pgxpool.Pool) http.Handler {
	authenticator := auth.NewSessionAuthenticator(pool)
	hub := realtime.NewHub()
	parents := parent.NewRepository(pool)
	usageRecorder := usage.NewRecorder(pool)
	plannerService := planner.NewService(pool)
	var voice classroom.VoiceProvider
	var speechHandler *speech.Handler
	if apiKey, sttModel, ttsModel, ttsVoice := os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_STT_MODEL"), os.Getenv("OPENAI_TTS_MODEL"), os.Getenv("OPENAI_TTS_VOICE"); apiKey != "" && sttModel != "" && ttsModel != "" && ttsVoice != "" {
		provider, err := speech.NewOpenAIProvider(&http.Client{Timeout: 90 * time.Second}, os.Getenv("OPENAI_BASE_URL"), apiKey, sttModel, ttsModel, ttsVoice)
		if err != nil {
			log.Fatalf("configure speech provider: %v", err)
		}
		voice = classroom.NewSpeechVoiceAdapter(provider)
		speechService, err := speech.NewService(provider, provider)
		if err != nil {
			log.Fatalf("configure speech service: %v", err)
		}
		speechHandler = speech.NewHandler(speechService, pool, usageRecorder)
	}
	classrooms := classroom.NewService(pool, hub, voice, usageRecorder, plannerService)
	go classrooms.RunStaleSessionRecovery(context.Background(), time.Minute)
	tutorConfig, err := loadTutorProviderConfig(os.Getenv)
	if err != nil {
		log.Fatalf("configure Tutor providers: %v", err)
	}
	if tutorConfig.Enabled {
		client, err := ai.NewStructuredProviderClient(
			&http.Client{Timeout: 90 * time.Second}, tutorConfig.Generator.BaseURL,
			tutorConfig.Generator.APIKey, tutorConfig.Generator.Provider, tutorConfig.Generator.Model,
			tutorConfig.Generator.Shape, tutorConfig.Generator.RequestOverlay,
		)
		if err != nil {
			log.Fatalf("configure teaching client: %v", err)
		}
		reviewClient, err := ai.NewStructuredProviderClient(
			&http.Client{Timeout: 90 * time.Second}, tutorConfig.Reviewer.BaseURL,
			tutorConfig.Reviewer.APIKey, tutorConfig.Reviewer.Provider, tutorConfig.Reviewer.Model,
			tutorConfig.Reviewer.Shape, tutorConfig.Reviewer.RequestOverlay,
		)
		if err != nil {
			log.Fatalf("configure Tutor output review client: %v", err)
		}
		reviewer, err := tutoraudit.NewOpenAIReviewer(
			reviewClient.WithUsageRecorder(usageRecorder),
			tutorConfig.Reviewer.Provider,
			tutorConfig.Reviewer.Model,
		)
		if err != nil {
			log.Fatalf("configure Tutor output reviewer: %v", err)
		}
		retryingReviewer, err := tutoraudit.NewRetryingReviewer(reviewer)
		if err != nil {
			log.Fatalf("configure Tutor output reviewer retry policy: %v", err)
		}
		auditor, err := tutoraudit.NewService(
			tutorConfig.Generator.identity(),
			tutorConfig.Reviewer.identity(),
			retryingReviewer,
			tutoraudit.NewPostgresRecorder(pool),
		)
		if err != nil {
			log.Fatalf("configure independent Tutor output audit: %v", err)
		}
		agent, err := ai.NewCodexProvider(client.WithUsageRecorder(usageRecorder), auditor)
		if err != nil {
			log.Fatalf("configure teaching agent: %v", err)
		}
		classrooms.WithTeachingAgent(agent)
	}
	pipelineRepository := contentpipeline.NewRepository(pool)
	var contentGenerator contentpipeline.ContentGenerator
	generatorIdentity := os.Getenv("CONTENT_GENERATOR_IDENTITY")
	if generatorIdentity == "" {
		generatorIdentity = "codex:content-generator"
	}
	if apiKey, generatorModel := os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_CONTENT_GENERATION_MODEL"); apiKey != "" && generatorModel != "" {
		client, err := ai.NewOpenAIResponsesClient(&http.Client{Timeout: 90 * time.Second}, os.Getenv("OPENAI_BASE_URL"), apiKey, generatorModel)
		if err != nil {
			log.Fatalf("configure content generation client: %v", err)
		}
		contentGenerator, err = contentpipeline.NewOpenAIGenerator(client.WithUsageRecorder(usageRecorder), "openai", generatorModel)
		if err != nil {
			log.Fatalf("configure content generator: %v", err)
		}
		generatorIdentity = "openai:" + generatorModel
	}
	var reviewService *contentpipeline.ReviewService
	if apiKey, reviewerModel := os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_CONTENT_REVIEW_MODEL"); apiKey != "" && reviewerModel != "" {
		client, err := ai.NewOpenAIResponsesClient(&http.Client{Timeout: 90 * time.Second}, os.Getenv("OPENAI_BASE_URL"), apiKey, reviewerModel)
		if err != nil {
			log.Fatalf("configure content review client: %v", err)
		}
		reviewer, err := contentpipeline.NewOpenAIReviewer(client.WithUsageRecorder(usageRecorder), "openai", reviewerModel)
		if err != nil {
			log.Fatalf("configure content reviewer: %v", err)
		}
		reviewService, err = contentpipeline.NewReviewService(generatorIdentity, "openai:"+reviewerModel, reviewer)
		if err != nil {
			log.Fatalf("configure independent content review: %v", err)
		}
	}
	pipelineService := contentpipeline.NewService(pipelineRepository, contentpipeline.Validator{}, reviewService).WithGenerator(contentGenerator)
	contentPipeline := contentpipeline.NewHandler(pipelineService)
	identityHandler := auth.NewHandler(pool, classrooms)
	if value := strings.TrimSpace(os.Getenv("SESSION_COOKIE_SECURE")); value == "1" || strings.EqualFold(value, "true") {
		identityHandler = auth.NewHandlerWithSecureCookies(pool, classrooms)
	}
	return api.NewRouter(api.Dependencies{
		Authenticate:    authenticator.Middleware,
		PublicQuestions: content.NewRepository(pool),
		Parents:         parents,
		Realtime:        realtime.NewWebSocketHandler(hub, realtime.DatabaseAuthorizer(pool)),
		Classroom:       classroom.NewHandler(classrooms, pool, parents, plannerService),
		Speech:          speechHandler,
		ContentPipeline: contentPipeline,
		Identity:        identityHandler,
		Trial:           trial.NewHandler(pool),
	})
}
