CREATE TABLE questions (
    id uuid PRIMARY KEY,
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id),
    template_id uuid,
    difficulty text NOT NULL CHECK (difficulty IN ('L0', 'L1', 'L2', 'L3', 'L4', 'L5')),
    question_type text NOT NULL,
    prompt_public text NOT NULL,
    scene_public_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    input_schema_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'AUTOMATIC_VALIDATED', 'AI_REVIEWED', 'RELEASED', 'QUARANTINED', 'REJECTED_AUTOMATIC')),
    content_version text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX questions_runtime_lookup ON questions (knowledge_point_id, difficulty) WHERE status = 'RELEASED';

CREATE TABLE question_private_answers (
    question_id uuid PRIMARY KEY REFERENCES questions(id) ON DELETE CASCADE,
    correct_answer_json jsonb NOT NULL,
    full_solution_private text NOT NULL,
    teacher_reference_answer text NOT NULL DEFAULT '',
    scoring_key_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    misconceptions_private_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    hint_policy_private_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

REVOKE ALL ON question_private_answers FROM PUBLIC;
