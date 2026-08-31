package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
)

func main() {
	email := flag.String("email", "", "normalized account email")
	roleValue := flag.String("role", "", "STUDENT, PARENT, or OWNER")
	displayName := flag.String("display-name", "", "display name")
	grade := flag.Int("grade", 0, "student grade from 1 through 9")
	studentLink := flag.String("student-id", "", "student UUID to bind to a new parent")
	flag.Parse()
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	password := os.Getenv("PROVISION_PASSWORD")
	if databaseURL == "" || password == "" {
		log.Fatal("DATABASE_URL and PROVISION_PASSWORD are required")
	}
	role, err := auth.ParseRole(strings.ToUpper(strings.TrimSpace(*roleValue)))
	if err != nil {
		log.Fatal(err)
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(*email))
	if normalizedEmail == "" || strings.TrimSpace(*displayName) == "" {
		log.Fatal("email and display-name are required")
	}
	if role == auth.RoleStudent && (*grade < 1 || *grade > 9) {
		log.Fatal("student grade must be between 1 and 9")
	}
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	userID, studentID := uuid.New(), uuid.Nil
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,role_code,email,display_name)VALUES($1,$2,$3,$4)`, userID, role, normalizedEmail, strings.TrimSpace(*displayName)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_credentials(user_id,password_hash)VALUES($1,$2)`, userID, passwordHash); err != nil {
			return err
		}
		switch role {
		case auth.RoleStudent:
			studentID = uuid.New()
			_, err = tx.Exec(ctx, `INSERT INTO students(id,user_id,grade_level)VALUES($1,$2,$3)`, studentID, userID, *grade)
			return err
		case auth.RoleParent:
			if strings.TrimSpace(*studentLink) == "" {
				return nil
			}
			linkedID, err := uuid.Parse(*studentLink)
			if err != nil {
				return fmt.Errorf("invalid student-id: %w", err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO parent_student_links(parent_user_id,student_id)VALUES($1,$2)`, userID, linkedID)
			return err
		default:
			return nil
		}
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Fatal("referenced student does not exist")
		}
		log.Fatal(err)
	}
	fmt.Printf("created role=%s user_id=%s", role, userID)
	if studentID != uuid.Nil {
		fmt.Printf(" student_id=%s", studentID)
	}
	fmt.Println()
}
