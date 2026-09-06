package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestOwnerCostReportFiltersAndSummary(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	ownerToken := "owner-" + uuid.NewString()
	ownerHash := sha256.Sum256([]byte(ownerToken))
	ownerID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,role_code,display_name) VALUES($1,'OWNER','成本管理员')`, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES($1,$2,$3,$4)`, uuid.New(), ownerID, ownerHash[:], time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	strongPrice, standardPrice := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO ai_price_catalog(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd,cost_tier)
VALUES($1,'openai','strong-model',now()-interval '1 day',1,0.5,2,'STRONG'),
      ($2,'openai','standard-model',now()-interval '1 day',1,0.5,2,'STANDARD')`, strongPrice, standardPrice); err != nil {
		t.Fatal(err)
	}
	otherUser, otherStudent, otherSession := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,role_code,display_name) VALUES($1,'STUDENT','其他学生')`, otherUser); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO students(id,user_id,grade_level) VALUES($1,$2,7)`, otherStudent, otherUser); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state,started_at) SELECT $1,$2,id,$3,'COMPLETED',10,'COMPLETE',now()-interval '1 day' FROM subjects WHERE code='ENGLISH'`, otherSession, otherStudent, fixture.releasedQuestionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='COMPLETED' WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ai_usage_records(request_id,student_id,session_id,provider,model,purpose,input_tokens,cached_input_tokens,output_tokens,audio_input_seconds,audio_output_seconds,estimated_cost_usd,price_catalog_id,created_at)
VALUES
('cost-a',$1,$2,'openai','strong-model','ANSWER_ANALYSIS',100,20,50,0,0,0.300000000,$4,current_date+interval '12 hours'),
('cost-b',$1,$2,'openai','standard-model','TTS_EXPLANATION',50,0,50,0,8,0.200000000,$5,current_date+interval '13 hours'),
('cost-c',$3,$6,'openai','standard-model','STT_TRANSCRIPTION',25,0,25,5,0,0.500000000,$5,current_date-interval '1 day'+interval '12 hours')`, fixture.studentID, fixture.sessionID, otherStudent, strongPrice, standardPrice, otherSession); err != nil {
		t.Fatal(err)
	}
	var knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT knowledge_point_id FROM questions WHERE id=$1`, fixture.releasedQuestionID).Scan(&knowledgePointID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO student_skill_states(student_id,knowledge_point_id,state) VALUES($1,$2,'MASTERED')`, fixture.studentID, knowledgePointID); err != nil {
		t.Fatal(err)
	}

	service := classroom.NewService(pool, nil, nil, nil)
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Classroom: classroom.NewHandler(service, pool, parent.NewRepository(pool))})
	var today string
	if err := pool.QueryRow(ctx, `SELECT current_date::text`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	filters := []struct {
		name, query  string
		wantRequests int64
		wantCost     string
	}{
		{"student", "student_id=" + fixture.studentID.String(), 2, "0.500000000"},
		{"session", "session_id=" + fixture.sessionID.String(), 2, "0.500000000"},
		{"subject", "subject=ENGLISH", 1, "0.500000000"},
		{"date", "date_from=" + today + "&date_to=" + today, 2, "0.500000000"},
		{"model", "model=strong-model", 1, "0.300000000"},
		{"purpose", "purpose=STT_TRANSCRIPTION", 1, "0.500000000"},
		{"combined", "student_id=" + fixture.studentID.String() + "&session_id=" + fixture.sessionID.String() + "&subject=MATH&date_from=" + today + "&date_to=" + today + "&model=strong-model&purpose=ANSWER_ANALYSIS", 1, "0.300000000"},
	}
	for _, test := range filters {
		t.Run(test.name, func(t *testing.T) {
			response := performJSON(router, http.MethodGet, "/api/v1/owner/costs?"+test.query, ownerToken, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("cost report=%d %s", response.Code, response.Body.String())
			}
			var report struct {
				Records []struct {
					Requests int64 `json:"requests"`
				} `json:"records"`
				Summary map[string]string `json:"summary"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			var requests int64
			for _, record := range report.Records {
				requests += record.Requests
			}
			if requests != test.wantRequests || report.Summary["total_cost_usd"] != test.wantCost {
				t.Fatalf("requests=%d cost=%s report=%s", requests, report.Summary["total_cost_usd"], response.Body.String())
			}
			if test.name == "combined" {
				assertCostSummary(t, report.Summary)
			}
		})
	}
}

func assertCostSummary(t *testing.T, summary map[string]string) {
	t.Helper()
	want := map[string]string{
		"total_cost_usd": "0.300000000", "cost_per_active_student_day_usd": "0.30000000000000000000",
		"cost_per_20_minute_lesson_usd": "0.30000000000000000000", "cost_per_mastered_skill_usd": "0.30000000000000000000",
		"cached_ratio": "0.20000000000000000000", "stt_cost_usd": "0", "tts_cost_usd": "0",
		"strong_model_ratio": "1.00000000000000000000", "average_tokens_per_request": "150.0000000000000000",
	}
	for key, value := range want {
		if summary[key] != value {
			t.Errorf("%s=%q want %q", key, summary[key], value)
		}
	}
}
