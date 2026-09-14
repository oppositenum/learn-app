package main

import (
	"context"
	"log"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/seedcontent"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("create database pool: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}
	actorID := uuid.Nil
	if raw := os.Getenv("CONTENT_SEED_ACTOR_ID"); raw != "" {
		actorID, err = uuid.Parse(raw)
		if err != nil {
			log.Fatalf("invalid CONTENT_SEED_ACTOR_ID: %v", err)
		}
	}
	activation, err := seedcontent.EnsureLinearEquation(ctx, pool, seedcontent.NewPipeline(pool), actorID)
	if err != nil {
		log.Fatalf("activate MATH-LINEAR-EQUATION: %v", err)
	}
	log.Printf("activated %s lineage=%s tasks=%d", seedcontent.LinearEquationKnowledgePointCode, activation.LineageID, len(activation.Tasks))
}
