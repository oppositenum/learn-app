package trial

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
)

type Willingness string

const (
	ContinueTomorrow Willingness = "CONTINUE_TOMORROW"
	Pause            Willingness = "PAUSE"
	Stop             Willingness = "STOP"
)

type Handler struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool, now: time.Now}
}

func (handler *Handler) SubmitReflection(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	sessionID, err := uuid.Parse(request.PathValue("session_id"))
	if err != nil {
		http.Error(writer, "invalid session id", http.StatusBadRequest)
		return
	}
	var body struct {
		Willingness Willingness `json:"willingness"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(writer, "invalid reflection", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(writer, "invalid reflection", http.StatusBadRequest)
		return
	}
	if body.Willingness != ContinueTomorrow && body.Willingness != Pause && body.Willingness != Stop {
		http.Error(writer, "invalid willingness", http.StatusBadRequest)
		return
	}
	var studentID uuid.UUID
	var endedAt time.Time
	err = handler.pool.QueryRow(request.Context(), `SELECT ls.student_id,ls.ended_at FROM learning_sessions ls JOIN students st ON st.id=ls.student_id WHERE ls.id=$1 AND st.user_id=$2 AND ls.status='COMPLETED'`, sessionID, userID).Scan(&studentID, &endedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(writer, "completed session not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(writer, "reflection unavailable", http.StatusInternalServerError)
		return
	}
	location, _ := time.LoadLocation("Asia/Shanghai")
	reflectionDate := endedAt.In(location).Format("2006-01-02")
	_, err = handler.pool.Exec(request.Context(), `INSERT INTO student_session_reflections(session_id,student_id,reflection_date,willingness) VALUES($1,$2,$3::date,$4) ON CONFLICT(session_id) DO UPDATE SET willingness=EXCLUDED.willingness,updated_at=now()`, sessionID, studentID, reflectionDate, body.Willingness)
	if err != nil {
		http.Error(writer, "reflection unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"saved": true, "willingness": body.Willingness})
}

type Day struct {
	Date              string      `json:"date"`
	CompletedSessions int         `json:"completed_sessions"`
	ActiveSeconds     int         `json:"active_seconds"`
	Willingness       Willingness `json:"willingness,omitempty"`
}

type StudentReport struct {
	StudentID            uuid.UUID `json:"student_id"`
	DisplayName          string    `json:"display_name"`
	ActivityStreakDays   int       `json:"longest_activity_streak_days"`
	WillingStreakDays    int       `json:"longest_willing_streak_days"`
	CurrentWillingDays   int       `json:"current_willing_streak_days"`
	SevenDayCoreComplete bool      `json:"seven_day_core_complete"`
	Days                 []Day     `json:"days"`
}

func (handler *Handler) OwnerReport(writer http.ResponseWriter, request *http.Request) {
	rows, err := handler.pool.Query(request.Context(), `
SELECT st.id,u.display_name,ad.activity_date,ad.completed_sessions,ad.active_seconds,COALESCE(ref.willingness,'')
FROM student_activity_days ad
JOIN students st ON st.id=ad.student_id
JOIN users u ON u.id=st.user_id
LEFT JOIN LATERAL (
    SELECT willingness FROM student_session_reflections sr
    WHERE sr.student_id=ad.student_id AND sr.reflection_date=ad.activity_date
    ORDER BY sr.updated_at DESC LIMIT 1
) ref ON true
ORDER BY st.id,ad.activity_date`)
	if err != nil {
		http.Error(writer, "trial report unavailable", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	reports := map[uuid.UUID]*StudentReport{}
	for rows.Next() {
		var studentID uuid.UUID
		var displayName string
		var date time.Time
		var day Day
		if err := rows.Scan(&studentID, &displayName, &date, &day.CompletedSessions, &day.ActiveSeconds, &day.Willingness); err != nil {
			http.Error(writer, "trial report unavailable", http.StatusInternalServerError)
			return
		}
		day.Date = date.Format("2006-01-02")
		if reports[studentID] == nil {
			reports[studentID] = &StudentReport{StudentID: studentID, DisplayName: displayName, Days: []Day{}}
		}
		reports[studentID].Days = append(reports[studentID].Days, day)
	}
	if err := rows.Err(); err != nil {
		http.Error(writer, "trial report unavailable", http.StatusInternalServerError)
		return
	}
	result := make([]StudentReport, 0, len(reports))
	for _, report := range reports {
		report.ActivityStreakDays = longestStreak(report.Days, false)
		report.WillingStreakDays = longestStreak(report.Days, true)
		report.CurrentWillingDays = trailingStreak(report.Days, true)
		report.SevenDayCoreComplete = report.WillingStreakDays >= 7
		result = append(result, *report)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DisplayName < result[j].DisplayName })
	writeJSON(writer, http.StatusOK, map[string]any{
		"criteria":     "seven consecutive completed activity days, each with a child-submitted CONTINUE_TOMORROW reflection",
		"generated_at": handler.now(),
		"records":      result,
	})
}

func longestStreak(days []Day, requireWilling bool) int {
	longest, current := 0, 0
	for index, day := range days {
		eligible := day.CompletedSessions > 0 && (!requireWilling || day.Willingness == ContinueTomorrow)
		if !eligible {
			current = 0
			continue
		}
		if index == 0 {
			current = 1
		} else {
			previous, _ := time.Parse("2006-01-02", days[index-1].Date)
			date, _ := time.Parse("2006-01-02", day.Date)
			if date.Sub(previous) == 24*time.Hour {
				current++
			} else {
				current = 1
			}
		}
		longest = max(longest, current)
	}
	return longest
}

func trailingStreak(days []Day, requireWilling bool) int {
	streak := 0
	for index := len(days) - 1; index >= 0; index-- {
		if days[index].CompletedSessions < 1 || (requireWilling && days[index].Willingness != ContinueTomorrow) {
			break
		}
		if index < len(days)-1 {
			current, _ := time.Parse("2006-01-02", days[index].Date)
			next, _ := time.Parse("2006-01-02", days[index+1].Date)
			if next.Sub(current) != 24*time.Hour {
				break
			}
		}
		streak++
	}
	return streak
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
