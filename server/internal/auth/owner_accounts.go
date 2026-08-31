package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ownerStudentAccount struct {
	UserID      uuid.UUID `json:"user_id"`
	StudentID   uuid.UUID `json:"student_id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	GradeLevel  int16     `json:"grade_level"`
	CreatedAt   time.Time `json:"created_at"`
}

type ownerParentAccount struct {
	UserID      uuid.UUID `json:"user_id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

type ownerParentStudentLink struct {
	ParentUserID uuid.UUID `json:"parent_user_id"`
	StudentID    uuid.UUID `json:"student_id"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

func (handler *Handler) OwnerAccounts(writer http.ResponseWriter, request *http.Request) {
	students := []ownerStudentAccount{}
	rows, err := handler.pool.Query(request.Context(), `
SELECT u.id,st.id,u.email,u.display_name,st.grade_level,u.created_at
FROM users u JOIN students st ON st.user_id=u.id
WHERE u.role_code='STUDENT' AND u.email IS NOT NULL
ORDER BY u.created_at DESC,u.id`)
	if err != nil {
		http.Error(writer, "accounts unavailable", http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var account ownerStudentAccount
		if err := rows.Scan(&account.UserID, &account.StudentID, &account.Email, &account.DisplayName, &account.GradeLevel, &account.CreatedAt); err != nil {
			rows.Close()
			http.Error(writer, "accounts unavailable", http.StatusInternalServerError)
			return
		}
		students = append(students, account)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		http.Error(writer, "accounts unavailable", http.StatusInternalServerError)
		return
	}
	rows.Close()

	parents := []ownerParentAccount{}
	rows, err = handler.pool.Query(request.Context(), `
SELECT id,email,display_name,created_at
FROM users
WHERE role_code='PARENT' AND email IS NOT NULL
ORDER BY created_at DESC,id`)
	if err != nil {
		http.Error(writer, "accounts unavailable", http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var account ownerParentAccount
		if err := rows.Scan(&account.UserID, &account.Email, &account.DisplayName, &account.CreatedAt); err != nil {
			rows.Close()
			http.Error(writer, "accounts unavailable", http.StatusInternalServerError)
			return
		}
		parents = append(parents, account)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		http.Error(writer, "accounts unavailable", http.StatusInternalServerError)
		return
	}
	rows.Close()

	links := []ownerParentStudentLink{}
	rows, err = handler.pool.Query(request.Context(), `
SELECT parent_user_id,student_id,status,created_at
FROM parent_student_links
ORDER BY created_at DESC,parent_user_id,student_id`)
	if err != nil {
		http.Error(writer, "accounts unavailable", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var link ownerParentStudentLink
		if err := rows.Scan(&link.ParentUserID, &link.StudentID, &link.Status, &link.CreatedAt); err != nil {
			http.Error(writer, "accounts unavailable", http.StatusInternalServerError)
			return
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		http.Error(writer, "accounts unavailable", http.StatusInternalServerError)
		return
	}
	writeAuthJSON(writer, http.StatusOK, map[string]any{"students": students, "parents": parents, "links": links})
}

func (handler *Handler) OwnerCreateStudent(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		GradeLevel  int16  `json:"grade_level"`
	}
	if err := decodeOwnerBody(writer, request, &body); err != nil {
		http.Error(writer, "invalid student account", http.StatusBadRequest)
		return
	}
	email, displayName, err := validateOwnerAccount(body.Email, body.DisplayName)
	if err != nil || body.GradeLevel < 1 || body.GradeLevel > 9 {
		http.Error(writer, "invalid student account", http.StatusBadRequest)
		return
	}
	passwordHash, err := HashPassword(body.Password)
	if err != nil {
		http.Error(writer, "password must contain 3 to 256 bytes", http.StatusBadRequest)
		return
	}
	userID, studentID := uuid.New(), uuid.New()
	err = pgx.BeginFunc(request.Context(), handler.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(request.Context(), `INSERT INTO users(id,role_code,email,display_name)VALUES($1,'STUDENT',$2,$3)`, userID, email, displayName); err != nil {
			return err
		}
		if _, err := tx.Exec(request.Context(), `INSERT INTO user_credentials(user_id,password_hash)VALUES($1,$2)`, userID, passwordHash); err != nil {
			return err
		}
		_, err := tx.Exec(request.Context(), `INSERT INTO students(id,user_id,grade_level)VALUES($1,$2,$3)`, studentID, userID, body.GradeLevel)
		return err
	})
	if ownerEmailConflict(err) {
		http.Error(writer, "email already exists", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(writer, "student account could not be created", http.StatusInternalServerError)
		return
	}
	writeAuthJSON(writer, http.StatusCreated, map[string]any{"user_id": userID, "student_id": studentID, "role": RoleStudent, "email": email, "display_name": displayName, "grade_level": body.GradeLevel})
}

func (handler *Handler) OwnerCreateParent(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Email       string   `json:"email"`
		DisplayName string   `json:"display_name"`
		Password    string   `json:"password"`
		StudentIDs  []string `json:"student_ids"`
	}
	if err := decodeOwnerBody(writer, request, &body); err != nil {
		http.Error(writer, "invalid parent account", http.StatusBadRequest)
		return
	}
	email, displayName, err := validateOwnerAccount(body.Email, body.DisplayName)
	studentIDs, studentErr := parseStudentIDs(body.StudentIDs)
	if err != nil || studentErr != nil || len(studentIDs) == 0 {
		http.Error(writer, "invalid parent account", http.StatusBadRequest)
		return
	}
	passwordHash, err := HashPassword(body.Password)
	if err != nil {
		http.Error(writer, "password must contain 3 to 256 bytes", http.StatusBadRequest)
		return
	}
	userID := uuid.New()
	err = pgx.BeginFunc(request.Context(), handler.pool, func(tx pgx.Tx) error {
		var found int
		if err := tx.QueryRow(request.Context(), `SELECT count(*) FROM students WHERE id=ANY($1)`, studentIDs).Scan(&found); err != nil {
			return err
		}
		if found != len(studentIDs) {
			return pgx.ErrNoRows
		}
		if _, err := tx.Exec(request.Context(), `INSERT INTO users(id,role_code,email,display_name)VALUES($1,'PARENT',$2,$3)`, userID, email, displayName); err != nil {
			return err
		}
		if _, err := tx.Exec(request.Context(), `INSERT INTO user_credentials(user_id,password_hash)VALUES($1,$2)`, userID, passwordHash); err != nil {
			return err
		}
		for _, studentID := range studentIDs {
			if _, err := tx.Exec(request.Context(), `INSERT INTO parent_student_links(parent_user_id,student_id)VALUES($1,$2)`, userID, studentID); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(writer, "student not found", http.StatusNotFound)
		return
	}
	if ownerEmailConflict(err) {
		http.Error(writer, "email already exists", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(writer, "parent account could not be created", http.StatusInternalServerError)
		return
	}
	writeAuthJSON(writer, http.StatusCreated, map[string]any{"user_id": userID, "role": RoleParent, "email": email, "display_name": displayName, "student_ids": studentIDs})
}

func (handler *Handler) OwnerCreateParentLink(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		ParentUserID string `json:"parent_user_id"`
		StudentID    string `json:"student_id"`
	}
	if err := decodeOwnerBody(writer, request, &body); err != nil {
		http.Error(writer, "invalid account link", http.StatusBadRequest)
		return
	}
	parentID, parentErr := uuid.Parse(body.ParentUserID)
	studentID, studentErr := uuid.Parse(body.StudentID)
	if parentErr != nil || studentErr != nil {
		http.Error(writer, "invalid account link", http.StatusBadRequest)
		return
	}
	result, err := handler.pool.Exec(request.Context(), `
INSERT INTO parent_student_links(parent_user_id,student_id)
SELECT $1,$2
WHERE EXISTS(SELECT 1 FROM users WHERE id=$1 AND role_code='PARENT')
  AND EXISTS(SELECT 1 FROM students WHERE id=$2)
ON CONFLICT(parent_user_id,student_id) DO UPDATE SET status='ACTIVE',revoked_at=NULL`, parentID, studentID)
	if err != nil {
		http.Error(writer, "account link could not be created", http.StatusInternalServerError)
		return
	}
	if result.RowsAffected() == 0 {
		http.Error(writer, "parent or student not found", http.StatusNotFound)
		return
	}
	writeAuthJSON(writer, http.StatusCreated, map[string]any{"parent_user_id": parentID, "student_id": studentID, "status": "ACTIVE"})
}

func decodeOwnerBody(writer http.ResponseWriter, request *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func validateOwnerAccount(rawEmail, rawDisplayName string) (string, string, error) {
	email := strings.ToLower(strings.TrimSpace(rawEmail))
	displayName := strings.TrimSpace(rawDisplayName)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 || len(displayName) == 0 || len(displayName) > 80 {
		return "", "", errors.New("invalid account details")
	}
	return email, displayName, nil
}

func parseStudentIDs(values []string) ([]uuid.UUID, error) {
	if len(values) > 20 {
		return nil, errors.New("too many students")
	}
	result := make([]uuid.UUID, 0, len(values))
	seen := map[uuid.UUID]bool{}
	for _, value := range values {
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result, nil
}

func ownerEmailConflict(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505" && databaseError.ConstraintName == "users_email_unique"
}
