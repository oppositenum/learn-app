CREATE TABLE subjects (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE CHECK (code IN ('MATH', 'CHINESE', 'ENGLISH', 'PHYSICS', 'CHEMISTRY')),
    name_zh text NOT NULL,
    sort_order smallint NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE'))
);

INSERT INTO subjects (id, code, name_zh, sort_order) VALUES
    ('00000000-0000-4000-8000-000000000001', 'MATH', '数学', 1),
    ('00000000-0000-4000-8000-000000000002', 'CHINESE', '语文', 2),
    ('00000000-0000-4000-8000-000000000003', 'ENGLISH', '英语', 3),
    ('00000000-0000-4000-8000-000000000004', 'PHYSICS', '物理', 4),
    ('00000000-0000-4000-8000-000000000005', 'CHEMISTRY', '化学', 5);

CREATE TABLE grade_bands (
    code text PRIMARY KEY CHECK (code IN ('PRIMARY', 'JUNIOR_SECONDARY')),
    name_zh text NOT NULL,
    min_grade smallint NOT NULL,
    max_grade smallint NOT NULL,
    CONSTRAINT grade_band_range_valid CHECK (min_grade BETWEEN 1 AND 9 AND max_grade BETWEEN min_grade AND 9)
);

INSERT INTO grade_bands (code, name_zh, min_grade, max_grade) VALUES
    ('PRIMARY', '小学', 1, 6),
    ('JUNIOR_SECONDARY', '初中', 7, 9);

CREATE TABLE domains (
    id uuid PRIMARY KEY,
    subject_id uuid NOT NULL REFERENCES subjects(id),
    code text NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    sort_order integer NOT NULL DEFAULT 0,
    UNIQUE (subject_id, code)
);

CREATE TABLE units (
    id uuid PRIMARY KEY,
    domain_id uuid NOT NULL REFERENCES domains(id) ON DELETE CASCADE,
    code text NOT NULL,
    name text NOT NULL,
    grade_band_code text NOT NULL REFERENCES grade_bands(code),
    sort_order integer NOT NULL DEFAULT 0,
    UNIQUE (domain_id, code)
);

CREATE TABLE knowledge_points (
    id uuid PRIMARY KEY,
    subject_id uuid NOT NULL REFERENCES subjects(id),
    domain_id uuid NOT NULL REFERENCES domains(id),
    unit_id uuid NOT NULL REFERENCES units(id),
    grade_band_code text NOT NULL REFERENCES grade_bands(code),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    why_it_matters_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    default_difficulty text NOT NULL CHECK (default_difficulty IN ('L0', 'L1', 'L2', 'L3', 'L4', 'L5')),
    status text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'RELEASED', 'QUARANTINED', 'RETIRED')),
    curriculum_version text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE knowledge_dependencies (
    source_knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    target_knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    relation text NOT NULL CHECK (relation IN ('REQUIRES', 'SUPPORTED_BY', 'EXTENDS')),
    strength numeric(4,3) NOT NULL CHECK (strength > 0 AND strength <= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_knowledge_point_id, target_knowledge_point_id, relation),
    CONSTRAINT knowledge_dependency_not_self CHECK (source_knowledge_point_id <> target_knowledge_point_id)
);

CREATE TABLE cross_subject_dependencies (
    id uuid PRIMARY KEY,
    source_knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    target_knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    relation text NOT NULL CHECK (relation IN ('REQUIRES', 'SUPPORTED_BY')),
    strength numeric(4,3) NOT NULL CHECK (strength > 0 AND strength <= 1),
    failure_signal_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    remediation_policy_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_knowledge_point_id, target_knowledge_point_id, relation),
    CONSTRAINT cross_subject_dependency_not_self CHECK (source_knowledge_point_id <> target_knowledge_point_id)
);

CREATE TABLE core_abilities (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    description text NOT NULL DEFAULT ''
);

CREATE TABLE knowledge_ability_links (
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    core_ability_id uuid NOT NULL REFERENCES core_abilities(id) ON DELETE CASCADE,
    weight numeric(4,3) NOT NULL CHECK (weight > 0 AND weight <= 1),
    PRIMARY KEY (knowledge_point_id, core_ability_id)
);

CREATE TABLE misconceptions (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    description text NOT NULL,
    diagnosis_signals_json jsonb NOT NULL DEFAULT '[]'::jsonb
);

CREATE TABLE knowledge_misconception_links (
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    misconception_id uuid NOT NULL REFERENCES misconceptions(id) ON DELETE CASCADE,
    PRIMARY KEY (knowledge_point_id, misconception_id)
);
