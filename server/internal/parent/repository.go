package parent

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (repository *Repository) CanSupervise(ctx context.Context, parentUserID, studentID uuid.UUID) (bool, error) {
	var allowed bool
	err := repository.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM parent_student_links l
    JOIN users p ON p.id = l.parent_user_id AND p.role_code = 'PARENT'
    JOIN students s ON s.id = l.student_id
    JOIN users child ON child.id = s.user_id AND child.role_code = 'STUDENT'
    WHERE l.parent_user_id = $1 AND l.student_id = $2 AND l.status = 'ACTIVE'
)`, parentUserID, studentID).Scan(&allowed)
	return allowed, err
}
