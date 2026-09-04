package classroom

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var learningLocation = func() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return location
}()

type activityQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func learningDate(now time.Time) string {
	return now.In(learningLocation).Format("2006-01-02")
}

func currentStreak(ctx context.Context, db activityQueryer, studentID uuid.UUID, now time.Time) (int, error) {
	today := now.In(learningLocation)
	rows, err := db.Query(ctx, `SELECT activity_date FROM student_activity_days WHERE student_id=$1 AND activity_date<=$2::date ORDER BY activity_date DESC`, studentID, learningDate(today))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	dates := make([]string, 0, 16)
	for rows.Next() {
		var date time.Time
		if err := rows.Scan(&date); err != nil {
			return 0, err
		}
		dates = append(dates, date.Format("2006-01-02"))
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(dates) == 0 {
		return 0, nil
	}
	expected := today
	if dates[0] != learningDate(expected) {
		expected = expected.AddDate(0, 0, -1)
		if dates[0] != learningDate(expected) {
			return 0, nil
		}
	}
	streak := 0
	for _, date := range dates {
		if date != learningDate(expected) {
			break
		}
		streak++
		expected = expected.AddDate(0, 0, -1)
	}
	return streak, nil
}
