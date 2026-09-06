CREATE TABLE misconception_quality_events (
    id uuid PRIMARY KEY,
    student_id uuid NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    session_id uuid NOT NULL REFERENCES learning_sessions(id) ON DELETE CASCADE,
    knowledge_point_id uuid NOT NULL REFERENCES knowledge_points(id) ON DELETE CASCADE,
    code_hash char(64) NOT NULL CHECK (code_hash ~ '^[0-9a-f]{64}$'),
    recognized_misconception_id uuid REFERENCES misconceptions(id) ON DELETE SET NULL,
    reason text NOT NULL CHECK (reason IN ('UNKNOWN_CODE', 'CROSS_KNOWLEDGE_POINT')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX misconception_quality_events_reporting
ON misconception_quality_events(reason, created_at DESC);
