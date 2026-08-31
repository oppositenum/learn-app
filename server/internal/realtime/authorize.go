package realtime

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
)

func DatabaseAuthorizer(pool *pgxpool.Pool) AuthorizeSubscription {
	parents := parent.NewRepository(pool)
	return func(ctx context.Context, principal auth.Principal, studentIDValue string) bool {
		studentID, err := uuid.Parse(studentIDValue)
		if err != nil {
			return false
		}
		userID, err := uuid.Parse(principal.UserID)
		if err != nil {
			return false
		}

		switch principal.Role {
		case auth.RoleStudent:
			var allowed bool
			err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM students WHERE id = $1 AND user_id = $2)`, studentID, userID).Scan(&allowed)
			return err == nil && allowed
		case auth.RoleParent:
			allowed, err := parents.CanSupervise(ctx, userID, studentID)
			return err == nil && allowed
		case auth.RoleOwner:
			return true
		default:
			return false
		}
	}
}
