CREATE TABLE IF NOT EXISTS answer_evaluation_provenance (
    id uuid PRIMARY KEY,
    student_answer_id uuid NOT NULL UNIQUE REFERENCES student_answers(id),
    deterministic_result text NOT NULL
        CHECK (deterministic_result IN ('MATCH', 'NO_MATCH')),
    deterministic_policy_version text NOT NULL
        CHECK (deterministic_policy_version <> ''),
    model_answer_correct boolean,
    model_confidence numeric(5,4)
        CHECK (model_confidence IS NULL OR model_confidence BETWEEN 0 AND 1),
    semantic_policy_version text,
    legacy_resolution text NOT NULL
        CHECK (legacy_resolution IN (
            'DETERMINISTIC_ACCEPTED',
            'MODEL_MEDIATED_ACCEPTED',
            'NOT_ACCEPTED'
        )),
    final_correct boolean NOT NULL,
    behavior_policy_version text NOT NULL
        CHECK (behavior_policy_version <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (model_answer_correct IS NULL AND model_confidence IS NULL AND semantic_policy_version IS NULL)
        OR
        (model_answer_correct IS NOT NULL AND model_confidence IS NOT NULL
            AND semantic_policy_version IS NOT NULL AND semantic_policy_version <> '')
    ),
    CHECK (
        (legacy_resolution = 'DETERMINISTIC_ACCEPTED' AND deterministic_result = 'MATCH' AND final_correct)
        OR
        (legacy_resolution = 'MODEL_MEDIATED_ACCEPTED' AND deterministic_result = 'NO_MATCH' AND final_correct)
        OR
        (legacy_resolution = 'NOT_ACCEPTED' AND deterministic_result = 'NO_MATCH' AND NOT final_correct)
    )
);

CREATE INDEX IF NOT EXISTS answer_evaluation_provenance_created
    ON answer_evaluation_provenance (created_at DESC);

CREATE TABLE IF NOT EXISTS mastery_evidence_provenance (
    id uuid PRIMARY KEY,
    student_answer_id uuid NOT NULL UNIQUE REFERENCES student_answers(id),
    answer_evaluation_provenance_id uuid UNIQUE
        REFERENCES answer_evaluation_provenance(id),
    student_id uuid NOT NULL REFERENCES students(id),
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id),
    evidence_form text NOT NULL
        CHECK (evidence_form IN ('LIFE', 'VARIANT', 'TEXTBOOK', 'REVIEW')),
    authorization_source text NOT NULL
        CHECK (authorization_source IN ('DETERMINISTIC_RULE', 'LEGACY_MODEL_MEDIATED')),
    provenance_risk text NOT NULL
        CHECK (provenance_risk IN ('NONE', 'UNREVIEWED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (authorization_source = 'DETERMINISTIC_RULE' AND provenance_risk = 'NONE')
        OR
        (authorization_source = 'LEGACY_MODEL_MEDIATED' AND provenance_risk = 'UNREVIEWED')
    )
);

CREATE INDEX IF NOT EXISTS mastery_evidence_provenance_student_skill
    ON mastery_evidence_provenance (student_id, knowledge_point_id, created_at DESC);

CREATE OR REPLACE FUNCTION reject_provenance_record_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'provenance records are append-only' USING ERRCODE = '55000';
END
$$;

DROP TRIGGER IF EXISTS answer_evaluation_provenance_append_only
    ON answer_evaluation_provenance;
CREATE TRIGGER answer_evaluation_provenance_append_only
BEFORE UPDATE OR DELETE ON answer_evaluation_provenance
FOR EACH ROW EXECUTE FUNCTION reject_provenance_record_mutation();

DROP TRIGGER IF EXISTS mastery_evidence_provenance_append_only
    ON mastery_evidence_provenance;
CREATE TRIGGER mastery_evidence_provenance_append_only
BEFORE UPDATE OR DELETE ON mastery_evidence_provenance
FOR EACH ROW EXECUTE FUNCTION reject_provenance_record_mutation();

CREATE OR REPLACE VIEW legacy_model_mediated_evidence_candidates AS
SELECT
    student_answer.id AS student_answer_id,
    session.student_id,
    question.knowledge_point_id,
    session.evidence_form
FROM student_answers student_answer
JOIN answer_analyses analysis
  ON analysis.student_answer_id = student_answer.id
JOIN learning_sessions session
  ON session.id = student_answer.session_id
JOIN questions question
  ON question.id = student_answer.question_id
JOIN question_private_answers private_answer
  ON private_answer.question_id = student_answer.question_id
WHERE analysis.answer_correct
  AND analysis.confidence >= 0.9
  AND regexp_replace(lower(student_answer.answer_text), '[[:space:]]+', '', 'g')
      <> regexp_replace(lower(private_answer.teacher_reference_answer), '[[:space:]]+', '', 'g')
  AND session.assistance_level = 0;

INSERT INTO mastery_evidence_provenance (
    id,
    student_answer_id,
    answer_evaluation_provenance_id,
    student_id,
    knowledge_point_id,
    evidence_form,
    authorization_source,
    provenance_risk
)
SELECT
    gen_random_uuid(),
    candidate.student_answer_id,
    NULL,
    candidate.student_id,
    candidate.knowledge_point_id,
    candidate.evidence_form,
    'LEGACY_MODEL_MEDIATED',
    'UNREVIEWED'
FROM legacy_model_mediated_evidence_candidates candidate
ON CONFLICT (student_answer_id) DO NOTHING;
