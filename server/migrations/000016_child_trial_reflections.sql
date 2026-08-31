CREATE TABLE student_session_reflections (
    session_id uuid PRIMARY KEY REFERENCES learning_sessions(id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    reflection_date date NOT NULL,
    willingness text NOT NULL CHECK (willingness IN ('CONTINUE_TOMORROW', 'PAUSE', 'STOP')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX student_session_reflections_trial_report
ON student_session_reflections (student_id, reflection_date DESC);

CREATE FUNCTION enforce_session_reflection_ownership() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    owner_student_id uuid;
    session_status text;
BEGIN
    SELECT student_id, status INTO owner_student_id, session_status
    FROM learning_sessions WHERE id = NEW.session_id;
    IF owner_student_id IS NULL OR owner_student_id <> NEW.student_id THEN
        RAISE EXCEPTION 'reflection student must own session';
    END IF;
    IF session_status <> 'COMPLETED' THEN
        RAISE EXCEPTION 'reflection requires a completed session';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER student_session_reflections_require_completed_owner
BEFORE INSERT OR UPDATE OF session_id, student_id ON student_session_reflections
FOR EACH ROW EXECUTE FUNCTION enforce_session_reflection_ownership();
