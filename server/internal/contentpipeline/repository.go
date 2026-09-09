package contentpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

type GenerationMetadata struct {
	Provider  string
	Model     string
	RequestID string
}

func (repository *Repository) CreateDraft(ctx context.Context, asset Asset, generation GenerationMetadata) error {
	return repository.CreateDrafts(ctx, []Asset{asset}, generation)
}

func (repository *Repository) CreateDrafts(ctx context.Context, assets []Asset, generation GenerationMetadata) error {
	if len(assets) == 0 {
		return errors.New("at least one draft asset is required")
	}
	return pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		for _, asset := range assets {
			if err := createDraft(ctx, tx, asset, generation); err != nil {
				return err
			}
		}
		return nil
	})
}

func createDraft(ctx context.Context, tx pgx.Tx, asset Asset, generation GenerationMetadata) error {
	questionID, err := uuid.Parse(asset.QuestionID)
	if err != nil {
		return fmt.Errorf("invalid question id: %w", err)
	}
	knowledgePointID, err := uuid.Parse(asset.KnowledgePointID)
	if err != nil {
		return fmt.Errorf("invalid knowledge point id: %w", err)
	}
	sourceID, err := uuid.Parse(asset.SourceID)
	if err != nil {
		return fmt.Errorf("invalid source id: %w", err)
	}
	assetJSON, err := json.Marshal(asset)
	if err != nil {
		return fmt.Errorf("encode content asset: %w", err)
	}
	answerJSON, err := json.Marshal(map[string]any{
		"value":         asset.TeacherPrivate.Answer,
		"numeric_value": asset.TeacherPrivate.NumericValue,
		"unit":          asset.TeacherPrivate.Unit,
	})
	if err != nil {
		return fmt.Errorf("encode private answer: %w", err)
	}
	misconceptionsJSON, err := json.Marshal(asset.TeacherPrivate.Misconceptions)
	if err != nil {
		return fmt.Errorf("encode misconceptions: %w", err)
	}
	publicChoices := make([]map[string]string, 0, len(asset.Choices))
	for _, choice := range asset.Choices {
		publicChoices = append(publicChoices, map[string]string{"id": choice.ID, "text": choice.Text})
	}
	sceneJSON, err := json.Marshal(map[string]any{"choices": publicChoices})
	if err != nil {
		return fmt.Errorf("encode public choices: %w", err)
	}
	if asset.QuestionType == "STRUCTURED_INTERACTION" {
		sceneJSON = asset.Scene
	}

	var subject string
	if err := tx.QueryRow(ctx, `SELECT s.code FROM knowledge_points kp JOIN subjects s ON s.id=kp.subject_id WHERE kp.id=$1 AND kp.status='RELEASED'`, knowledgePointID).Scan(&subject); err != nil {
		return fmt.Errorf("released knowledge point not found: %w", err)
	}
	if subject != asset.SubjectCode {
		return errors.New("asset subject does not match knowledge point")
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO questions(id,knowledge_point_id,difficulty,question_type,prompt_public,scene_public_json,input_schema_json,status,content_version)
VALUES($1,$2,$3,$4,$5,$6,$7,'DRAFT',$8)`, questionID, knowledgePointID, asset.Difficulty, asset.QuestionType, asset.PromptPublic, sceneJSON, asset.InputSchema, asset.ContentVersion); err != nil {
		return fmt.Errorf("insert draft question: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO question_private_answers(question_id,correct_answer_json,full_solution_private,teacher_reference_answer,misconceptions_private_json)
VALUES($1,$2,$3,$4,$5)`, questionID, answerJSON, asset.TeacherPrivate.Solution, asset.TeacherPrivate.Answer, misconceptionsJSON); err != nil {
		return fmt.Errorf("insert private answer: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO content_versions(id,question_id,version,schema_version,generator_provider,generator_model,generator_request_id,source_id,asset_json)
VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9)`, uuid.New(), questionID, asset.ContentVersion, asset.SchemaVersion, generation.Provider, generation.Model, generation.RequestID, sourceID, assetJSON); err != nil {
		return fmt.Errorf("insert content version: %w", err)
	}
	return nil
}

func (repository *Repository) GenerationOptions(ctx context.Context) ([]KnowledgePointOption, []SourceOption, error) {
	rows, err := repository.pool.Query(ctx, `
SELECT kp.id,s.code,s.name_zh,kp.code,kp.name,kp.description,kp.grade_band_code,gb.name_zh,
       d.code,d.name,u.code,u.name,kp.default_difficulty,provenance.source_name,provenance.source_ref
FROM knowledge_points kp
JOIN subjects s ON s.id=kp.subject_id
JOIN grade_bands gb ON gb.code=kp.grade_band_code
JOIN domains d ON d.id=kp.domain_id AND d.subject_id=kp.subject_id
JOIN units u ON u.id=kp.unit_id AND u.domain_id=kp.domain_id AND u.grade_band_code=kp.grade_band_code
JOIN LATERAL (
    SELECT cs.name AS source_name,kps.source_ref
    FROM knowledge_point_sources kps
    JOIN curriculum_sources cs ON cs.id=kps.curriculum_source_id AND cs.status='RELEASED'
    WHERE kps.knowledge_point_id=kp.id
    ORDER BY CASE kps.basis_kind WHEN 'V1_SKELETON' THEN 0 ELSE 1 END,kps.source_ref
    LIMIT 1
) provenance ON true
WHERE kp.status='RELEASED' AND s.status='ACTIVE'
ORDER BY s.sort_order,gb.min_grade,d.sort_order,u.sort_order,kp.name,kp.id`)
	if err != nil {
		return nil, nil, fmt.Errorf("list released knowledge points: %w", err)
	}
	defer rows.Close()
	knowledgePoints := []KnowledgePointOption{}
	for rows.Next() {
		var item KnowledgePointOption
		if err := rows.Scan(
			&item.ID, &item.SubjectCode, &item.SubjectName, &item.KnowledgePointCode, &item.Name,
			&item.Description, &item.GradeBandCode, &item.GradeBandName, &item.DomainCode, &item.DomainName,
			&item.UnitCode, &item.UnitName, &item.DefaultDifficulty, &item.CurriculumSourceName, &item.CurriculumSourceRef,
		); err != nil {
			return nil, nil, fmt.Errorf("scan knowledge point option: %w", err)
		}
		knowledgePoints = append(knowledgePoints, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate knowledge point options: %w", err)
	}

	sourceRows, err := repository.pool.Query(ctx, `
SELECT id,name,source_type,license_code,attribution
FROM content_sources
WHERE btrim(license_code)<>''
ORDER BY name,id`)
	if err != nil {
		return nil, nil, fmt.Errorf("list content sources: %w", err)
	}
	defer sourceRows.Close()
	sources := []SourceOption{}
	for sourceRows.Next() {
		var item SourceOption
		if err := sourceRows.Scan(&item.ID, &item.Name, &item.SourceType, &item.LicenseCode, &item.Attribution); err != nil {
			return nil, nil, fmt.Errorf("scan content source option: %w", err)
		}
		sources = append(sources, item)
	}
	if err := sourceRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate content source options: %w", err)
	}
	return knowledgePoints, sources, nil
}

func (repository *Repository) GenerationContext(ctx context.Context, request GenerateRequest) (GenerationContext, error) {
	knowledgePointID, _ := uuid.Parse(request.KnowledgePointID)
	sourceID, _ := uuid.Parse(request.SourceID)
	var input GenerationContext
	input.KnowledgePointID = knowledgePointID.String()
	input.Difficulty = request.Difficulty
	input.QuestionType = request.QuestionType
	input.Count = request.Count
	input.Requirements = strings.TrimSpace(request.Requirements)
	err := repository.pool.QueryRow(ctx, `
SELECT s.code,s.name_zh,kp.code,kp.name,kp.description,kp.grade_band_code,kp.why_it_matters_json,
       d.name,u.name,curriculum_source.name,curriculum_source.source_ref,
       cs.name,cs.source_type,cs.license_code,cs.attribution
FROM knowledge_points kp
JOIN subjects s ON s.id=kp.subject_id AND s.status='ACTIVE'
JOIN domains d ON d.id=kp.domain_id AND d.subject_id=kp.subject_id
JOIN units u ON u.id=kp.unit_id AND u.domain_id=kp.domain_id AND u.grade_band_code=kp.grade_band_code
JOIN LATERAL (
    SELECT source.name,kps.source_ref
    FROM knowledge_point_sources kps
    JOIN curriculum_sources source ON source.id=kps.curriculum_source_id AND source.status='RELEASED'
    WHERE kps.knowledge_point_id=kp.id
    ORDER BY CASE kps.basis_kind WHEN 'V1_SKELETON' THEN 0 ELSE 1 END,kps.source_ref
    LIMIT 1
) curriculum_source ON true
CROSS JOIN content_sources cs
WHERE kp.id=$1 AND kp.status='RELEASED' AND cs.id=$2 AND btrim(cs.license_code)<>''`, knowledgePointID, sourceID).Scan(
		&input.SubjectCode, &input.SubjectName, &input.KnowledgePointCode, &input.KnowledgePointName,
		&input.KnowledgeDescription, &input.GradeBandCode, &input.WhyItMatters, &input.DomainName,
		&input.UnitName, &input.CurriculumSourceName, &input.CurriculumSourceRef, &input.SourceName,
		&input.SourceType, &input.SourceLicense, &input.SourceAttribution,
	)
	if err != nil {
		return GenerationContext{}, err
	}
	return input, nil
}

func (repository *Repository) LoadAsset(ctx context.Context, questionID uuid.UUID) (Asset, GenerationMetadata, error) {
	var raw []byte
	var generation GenerationMetadata
	err := repository.pool.QueryRow(ctx, `
SELECT cv.asset_json,cv.generator_provider,cv.generator_model,COALESCE(cv.generator_request_id,'')
FROM questions q JOIN content_versions cv ON cv.question_id=q.id AND cv.version=q.content_version
WHERE q.id=$1`, questionID).Scan(&raw, &generation.Provider, &generation.Model, &generation.RequestID)
	if err != nil {
		return Asset{}, GenerationMetadata{}, err
	}
	var asset Asset
	if err := json.Unmarshal(raw, &asset); err != nil {
		return Asset{}, GenerationMetadata{}, fmt.Errorf("decode content asset: %w", err)
	}
	return asset, generation, nil
}

func (repository *Repository) DuplicateExists(ctx context.Context, normalizedHash string, exceptQuestionID uuid.UUID) (bool, error) {
	rows, err := repository.pool.Query(ctx, `SELECT id,prompt_public FROM questions WHERE id<>$1`, exceptQuestionID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var prompt string
		if err := rows.Scan(&id, &prompt); err != nil {
			return false, err
		}
		if NormalizedPromptHash(prompt) == normalizedHash {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (repository *Repository) ReleaseEvidence(ctx context.Context, questionID uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	var validationID, reviewID uuid.UUID
	err := repository.pool.QueryRow(ctx, `
SELECT v.id,r.id FROM questions q
JOIN content_validations v ON v.question_id=q.id AND v.content_version=q.content_version AND v.schema_version=$2 AND v.status='PASS'
JOIN content_reviews r ON r.question_id=q.id AND r.content_version=q.content_version AND r.schema_version=v.schema_version AND r.result='PASS'
WHERE q.id=$1 AND q.status='AI_REVIEWED'
ORDER BY v.created_at DESC,r.created_at DESC LIMIT 1`, questionID, CurrentSchemaVersion).Scan(&validationID, &reviewID)
	return validationID, reviewID, err
}

func (repository *Repository) RecordValidation(ctx context.Context, questionID uuid.UUID, contentVersion, schemaVersion string, validation Validation) (uuid.UUID, Status, error) {
	status := "REJECTED_AUTOMATIC"
	next := RejectedAutomatic
	if validation.Passed {
		status, next = "PASS", AutomaticValidated
	}
	checks, err := json.Marshal(validation.Checks)
	if err != nil {
		return uuid.Nil, "", err
	}
	id := uuid.New()
	err = pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		var current Status
		if err := tx.QueryRow(ctx, `SELECT status FROM questions WHERE id = $1 FOR UPDATE`, questionID).Scan(&current); err != nil {
			return err
		}
		if current != Draft {
			return ErrReleaseGate
		}
		if _, err := tx.Exec(ctx, `INSERT INTO content_validations (id, question_id, content_version, schema_version, status, checks_json, validator_version) VALUES ($1,$2,$3,$4,$5,$6,'validator-v1')`, id, questionID, contentVersion, schemaVersion, status, checks); err != nil {
			return err
		}
		command, err := tx.Exec(ctx, `UPDATE questions SET status=$2, updated_at=now() WHERE id=$1 AND content_version=$3`, questionID, next, contentVersion)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return errors.New("question version changed")
		}
		return nil
	})
	return id, next, err
}

func (repository *Repository) RecordReview(ctx context.Context, questionID uuid.UUID, contentVersion, schemaVersion, provider, model, requestID string, review Review) (uuid.UUID, Status, error) {
	findings, err := json.Marshal(review.Findings)
	if err != nil {
		return uuid.Nil, "", err
	}
	id := uuid.New()
	next := AutomaticValidated
	passed := review.Result == ReviewPass && review.AgeAppropriate && review.FactuallySound && review.Unambiguous && review.NoAnswerLeak && review.SafeValues
	err = pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		var current Status
		if err := tx.QueryRow(ctx, `SELECT status FROM questions WHERE id=$1 FOR UPDATE`, questionID).Scan(&current); err != nil {
			return err
		}
		if current != AutomaticValidated {
			return ErrReleaseGate
		}
		if _, err := tx.Exec(ctx, `INSERT INTO content_reviews (id,question_id,content_version,schema_version,result,reviewer_provider,reviewer_model,reviewer_request_id,findings_json) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, questionID, contentVersion, schemaVersion, review.Result, provider, model, requestID, findings); err != nil {
			return err
		}
		if !passed {
			return nil
		}
		command, err := tx.Exec(ctx, `UPDATE questions SET status='AI_REVIEWED', updated_at=now() WHERE id=$1 AND content_version=$2`, questionID, contentVersion)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return errors.New("question version changed")
		}
		next = AIReviewed
		return nil
	})
	return id, next, err
}

func (repository *Repository) Release(ctx context.Context, questionID, actorID, validationID, reviewID uuid.UUID, reason string) error {
	if reason == "" {
		return errors.New("release reason is required")
	}
	return pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		var current Status
		if err := tx.QueryRow(ctx, `SELECT status FROM questions WHERE id=$1 FOR UPDATE`, questionID).Scan(&current); err != nil {
			return err
		}
		if current != AIReviewed {
			return ErrReleaseGate
		}
		if _, err := tx.Exec(ctx, `UPDATE questions SET status='RELEASED', updated_at=now() WHERE id=$1`, questionID); err != nil {
			return fmt.Errorf("release gate: %w", err)
		}
		_, err := tx.Exec(ctx, `INSERT INTO content_release_records (id,question_id,from_status,to_status,validation_id,review_id,reason,actor_user_id) VALUES ($1,$2,'AI_REVIEWED','RELEASED',$3,$4,$5,$6)`, uuid.New(), questionID, validationID, reviewID, reason, nullableUUID(actorID))
		return err
	})
}

func (repository *Repository) Quarantine(ctx context.Context, questionID, actorID uuid.UUID, reason string) error {
	if reason == "" {
		return errors.New("quarantine reason is required")
	}
	return pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		command, err := tx.Exec(ctx, `UPDATE questions SET status='QUARANTINED', updated_at=now() WHERE id=$1 AND status='RELEASED'`, questionID)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrReleaseGate
		}
		_, err = tx.Exec(ctx, `INSERT INTO content_release_records (id,question_id,from_status,to_status,reason,actor_user_id) VALUES ($1,$2,'RELEASED','QUARANTINED',$3,$4)`, uuid.New(), questionID, reason, nullableUUID(actorID))
		return err
	})
}

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
