CREATE TABLE student_activity_days (
    student_id uuid NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    activity_date date NOT NULL,
    completed_sessions integer NOT NULL DEFAULT 0 CHECK (completed_sessions >= 0),
    active_seconds integer NOT NULL DEFAULT 0 CHECK (active_seconds >= 0),
    first_completed_at timestamptz NOT NULL,
    last_completed_at timestamptz NOT NULL,
    PRIMARY KEY (student_id, activity_date)
);

CREATE INDEX student_activity_days_recent
ON student_activity_days (student_id, activity_date DESC);
