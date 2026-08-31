CREATE TABLE roles (
    code text PRIMARY KEY,
    description text NOT NULL
);

INSERT INTO roles (code, description) VALUES
    ('STUDENT', 'Child learning account'),
    ('PARENT', 'Guardian supervision account'),
    ('OWNER', 'Content and operations administrator');

CREATE TABLE users (
    id uuid PRIMARY KEY,
    role_code text NOT NULL REFERENCES roles(code),
    email text,
    display_name text NOT NULL CHECK (length(trim(display_name)) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_normalized CHECK (email IS NULL OR email = lower(email))
);

CREATE UNIQUE INDEX users_email_unique ON users (email) WHERE email IS NOT NULL;

CREATE TABLE students (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    grade_level smallint NOT NULL CHECK (grade_level BETWEEN 1 AND 9),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION enforce_student_user_role() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND role_code = 'STUDENT') THEN
        RAISE EXCEPTION 'student profile requires a STUDENT user';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER students_require_student_role
BEFORE INSERT OR UPDATE OF user_id ON students
FOR EACH ROW EXECUTE FUNCTION enforce_student_user_role();

CREATE TABLE parent_student_links (
    parent_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'REVOKED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    PRIMARY KEY (parent_user_id, student_id),
    CONSTRAINT parent_student_link_revocation_consistent CHECK (
        (status = 'ACTIVE' AND revoked_at IS NULL)
        OR (status = 'REVOKED' AND revoked_at IS NOT NULL)
    )
);

CREATE FUNCTION enforce_parent_student_link_roles() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM users WHERE id = NEW.parent_user_id AND role_code = 'PARENT') THEN
        RAISE EXCEPTION 'parent_student_link requires a PARENT user';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM students s
        JOIN users u ON u.id = s.user_id
        WHERE s.id = NEW.student_id AND u.role_code = 'STUDENT'
    ) THEN
        RAISE EXCEPTION 'parent_student_link requires a STUDENT profile';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER parent_student_links_require_roles
BEFORE INSERT OR UPDATE OF parent_user_id, student_id ON parent_student_links
FOR EACH ROW EXECUTE FUNCTION enforce_parent_student_link_roles();

CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    CONSTRAINT sessions_expiry_after_creation CHECK (expires_at > created_at)
);

CREATE INDEX sessions_active_lookup ON sessions (token_hash, expires_at) WHERE revoked_at IS NULL;
