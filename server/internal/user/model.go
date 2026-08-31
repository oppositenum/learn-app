package user

import (
	"time"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
)

type User struct {
	ID          uuid.UUID
	Role        auth.Role
	Email       *string
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Student struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	GradeLevel int16
	CreatedAt  time.Time
}
