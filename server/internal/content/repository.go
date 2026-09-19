package content

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrQuestionNotFound = errors.New("question not found")

type PublicQuestionReader interface {
	ReleasedPublicQuestionForStudent(ctx context.Context, id, studentUserID uuid.UUID) (QuestionPublic, error)
}

type TeachingQuestionReader interface {
	ReleasedQuestionForTeaching(ctx context.Context, id uuid.UUID) (QuestionForTeaching, error)
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const releasedPublicQuestionQuery = `
SELECT id, knowledge_point_id, difficulty, question_type, prompt_public,
       scene_public_json, input_schema_json, content_version
FROM questions
WHERE id = $1 AND status = 'RELEASED'`

func (repository *Repository) ReleasedPublicQuestion(ctx context.Context, id uuid.UUID) (QuestionPublic, error) {
	var question QuestionPublic
	err := repository.pool.QueryRow(ctx, releasedPublicQuestionQuery, id).Scan(
		&question.ID,
		&question.KnowledgePointID,
		&question.Difficulty,
		&question.QuestionType,
		&question.Prompt,
		&question.Scene,
		&question.InputSchema,
		&question.ContentVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuestionPublic{}, ErrQuestionNotFound
	}
	return question, err
}

const releasedPublicQuestionForStudentQuery = `
SELECT q.id, q.knowledge_point_id, q.difficulty, q.question_type, q.prompt_public,
       q.scene_public_json, q.input_schema_json, q.content_version
FROM questions q
JOIN knowledge_points knowledge_point ON knowledge_point.id=q.knowledge_point_id AND knowledge_point.status='RELEASED'
JOIN grade_bands grade_band ON grade_band.code=knowledge_point.grade_band_code
JOIN students student ON student.user_id=$2
WHERE q.id=$1 AND q.status='RELEASED' AND grade_band.min_grade<=student.grade_level`

func (repository *Repository) ReleasedPublicQuestionForStudent(ctx context.Context, id, studentUserID uuid.UUID) (QuestionPublic, error) {
	var question QuestionPublic
	err := repository.pool.QueryRow(ctx, releasedPublicQuestionForStudentQuery, id, studentUserID).Scan(
		&question.ID,
		&question.KnowledgePointID,
		&question.Difficulty,
		&question.QuestionType,
		&question.Prompt,
		&question.Scene,
		&question.InputSchema,
		&question.ContentVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuestionPublic{}, ErrQuestionNotFound
	}
	return question, err
}

const releasedTeachingQuestionQuery = `
SELECT q.id, q.knowledge_point_id, q.difficulty, q.question_type, q.prompt_public,
       q.scene_public_json, q.input_schema_json, q.content_version,
       a.correct_answer_json, a.full_solution_private, a.teacher_reference_answer,
       a.scoring_key_json, a.misconceptions_private_json, a.hint_policy_private_json,
       a.created_at, a.updated_at,
       s.code, s.name_zh, kp.code, kp.name
FROM questions q
JOIN question_private_answers a ON a.question_id = q.id
JOIN knowledge_points kp ON kp.id = q.knowledge_point_id
JOIN subjects s ON s.id = kp.subject_id
WHERE q.id = $1 AND q.status = 'RELEASED'`

func (repository *Repository) ReleasedQuestionForTeaching(ctx context.Context, id uuid.UUID) (QuestionForTeaching, error) {
	var question QuestionForTeaching
	err := repository.pool.QueryRow(ctx, releasedTeachingQuestionQuery, id).Scan(
		&question.Public.ID,
		&question.Public.KnowledgePointID,
		&question.Public.Difficulty,
		&question.Public.QuestionType,
		&question.Public.Prompt,
		&question.Public.Scene,
		&question.Public.InputSchema,
		&question.Public.ContentVersion,
		&question.Private.CorrectAnswer,
		&question.Private.FullSolution,
		&question.Private.TeacherReferenceAnswer,
		&question.Private.ScoringKey,
		&question.Private.Misconceptions,
		&question.Private.HintPolicy,
		&question.Private.CreatedAt,
		&question.Private.UpdatedAt,
		&question.Teaching.SubjectCode,
		&question.Teaching.SubjectName,
		&question.Teaching.KnowledgePointCode,
		&question.Teaching.KnowledgePointName,
	)
	question.Private.QuestionID = question.Public.ID
	if errors.Is(err, pgx.ErrNoRows) {
		return QuestionForTeaching{}, ErrQuestionNotFound
	}
	return question, err
}
