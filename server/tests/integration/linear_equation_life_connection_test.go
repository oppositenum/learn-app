package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

const lifeConnectionMigration = "000041_linear_equation_life_connection.sql"

// The four life-connection items written for MATH-LINEAR-EQUATION.
const (
	linearEquationDailyLife       = "用来把固定费用和按数量增加的费用分开，比如门票加服务费、租车起步价加每公里费用。"
	linearEquationHumanWorld      = "用来比较两种计费哪个更合适，比如两家打印店、两种租车方案。"
	linearEquationFutureLearning  = "后面学习一次函数时，会用这里的固定起点和每增加 1 份的变化。"
	linearEquationCareerOrScience = "工程、财务和实验记录里，常用这种固定量加相同变化量来估算。"
)

type lifeConnectionScene struct {
	ConnectionType string `json:"connection_type"`
	Title          string `json:"title"`
	Explanation    string `json:"explanation"`
}

var linearEquationScenes = []lifeConnectionScene{
	{ConnectionType: "DAILY_LIFE", Title: "门票加服务费、租车起步价加每公里费用", Explanation: linearEquationDailyLife},
	{ConnectionType: "HUMAN_WORLD", Title: "两家打印店、两种租车方案", Explanation: linearEquationHumanWorld},
	{ConnectionType: "SCIENCE_OR_CAREER", Title: "工程、财务和实验记录", Explanation: linearEquationCareerOrScience},
}

// lifeConnectionFingerprint digests what the life-connection migration must
// leave alone: every other knowledge point's items and scenes, and the
// questions and private answers of the linear equation itself.
type lifeConnectionFingerprint struct {
	otherItems, otherScenes, ownQuestions string
	otherSceneRows, ownQuestionRows       int
}

func takeLifeConnectionFingerprint(t *testing.T, ctx context.Context, pool *pgxpool.Pool) lifeConnectionFingerprint {
	t.Helper()
	var fingerprint lifeConnectionFingerprint
	if err := pool.QueryRow(ctx, `
SELECT
  (SELECT md5(string_agg(kp.code || '=' || kp.why_it_matters_json::text, '|' ORDER BY kp.code)) FROM knowledge_points kp WHERE kp.code<>'MATH-LINEAR-EQUATION'),
  (SELECT md5(string_agg(wc.id::text || wc.connection_type || wc.title || wc.explanation, '|' ORDER BY wc.id)) FROM world_connections wc JOIN knowledge_points kp ON kp.id=wc.knowledge_point_id WHERE kp.code<>'MATH-LINEAR-EQUATION'),
  (SELECT count(*) FROM world_connections wc JOIN knowledge_points kp ON kp.id=wc.knowledge_point_id WHERE kp.code<>'MATH-LINEAR-EQUATION'),
  (SELECT md5(COALESCE(string_agg(q.id::text || q.status || q.prompt_public || q.scene_public_json::text || COALESCE(a.correct_answer_json::text,'') || COALESCE(a.full_solution_private,''), '|' ORDER BY q.id), '')) FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id LEFT JOIN question_private_answers a ON a.question_id=q.id WHERE kp.code='MATH-LINEAR-EQUATION'),
  (SELECT count(*) FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id WHERE kp.code='MATH-LINEAR-EQUATION')`).Scan(
		&fingerprint.otherItems, &fingerprint.otherScenes, &fingerprint.otherSceneRows, &fingerprint.ownQuestions, &fingerprint.ownQuestionRows); err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func TestLinearEquationLifeConnectionChangesOnlyItsOwnItemsAndScenes(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrationsBefore(t, lifeConnectionMigration)); err != nil {
		t.Fatal(err)
	}
	before := takeLifeConnectionFingerprint(t, ctx, pool)
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	after := takeLifeConnectionFingerprint(t, ctx, pool)
	if before != after {
		t.Fatalf("life-connection migration touched other knowledge points or the linear equation questions: before=%+v after=%+v", before, after)
	}
	if after.otherSceneRows == 0 {
		t.Fatal("no other knowledge point has scenes to compare")
	}

	var items map[string]string
	if err := pool.QueryRow(ctx, `SELECT why_it_matters_json FROM knowledge_points WHERE code='MATH-LINEAR-EQUATION'`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"daily_life": linearEquationDailyLife, "human_world": linearEquationHumanWorld, "future_learning": linearEquationFutureLearning, "career_or_science": linearEquationCareerOrScience}
	if len(items) != len(want) {
		t.Fatalf("why_it_matters keys=%d want %d", len(items), len(want))
	}
	for key, sentence := range want {
		if items[key] != sentence {
			t.Fatalf("why_it_matters %s=%q", key, items[key])
		}
	}

	rows, err := pool.Query(ctx, `
SELECT wc.connection_type,wc.title,wc.explanation FROM world_connections wc JOIN knowledge_points kp ON kp.id=wc.knowledge_point_id
WHERE kp.code='MATH-LINEAR-EQUATION' ORDER BY wc.connection_type`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	scenes := []lifeConnectionScene{}
	for rows.Next() {
		var scene lifeConnectionScene
		if err := rows.Scan(&scene.ConnectionType, &scene.Title, &scene.Explanation); err != nil {
			t.Fatal(err)
		}
		scenes = append(scenes, scene)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(scenes) != len(linearEquationScenes) {
		t.Fatalf("linear equation scenes=%d want one per type", len(scenes))
	}
	for index, scene := range scenes {
		if scene != linearEquationScenes[index] {
			t.Fatalf("scene %d=%+v want %+v", index, scene, linearEquationScenes[index])
		}
	}
}

// The child classroom carries only the daily-life sentence; the Owner content
// page carries all four items and the three scenes.
func TestStudentClassroomShowsOnlyDailyLifeWhileOwnerSeesWholeLifeConnection(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture, activation := seedLinearEquationActivation(t, ctx, pool)
	router := stageRouter(pool, classroom.NewService(pool, nil, nil, nil, planner.NewService(pool)).WithTeachingAgent(&provenanceTeachingAgent{}))
	ownerToken := seedOwner(t, ctx, pool)

	response := performJSON(router, http.MethodGet, "/api/v1/owner/content", ownerToken, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("owner content=%d %s", response.Code, response.Body.String())
	}
	var report struct {
		KnowledgePoints []struct {
			KnowledgePointCode string                `json:"knowledge_point_code"`
			WhyItMatters       map[string]string     `json:"why_it_matters"`
			WorldConnections   []lifeConnectionScene `json:"world_connections"`
		} `json:"knowledge_points"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.KnowledgePoints) != 1 || report.KnowledgePoints[0].KnowledgePointCode != "MATH-LINEAR-EQUATION" {
		t.Fatalf("owner content knowledge points=%+v", report.KnowledgePoints)
	}
	point := report.KnowledgePoints[0]
	if point.WhyItMatters["daily_life"] != linearEquationDailyLife || point.WhyItMatters["human_world"] != linearEquationHumanWorld ||
		point.WhyItMatters["future_learning"] != linearEquationFutureLearning || point.WhyItMatters["career_or_science"] != linearEquationCareerOrScience {
		t.Fatalf("owner why_it_matters=%+v", point.WhyItMatters)
	}
	if len(point.WorldConnections) != len(linearEquationScenes) {
		t.Fatalf("owner scenes=%d", len(point.WorldConnections))
	}
	for index, scene := range point.WorldConnections {
		if scene != linearEquationScenes[index] {
			t.Fatalf("owner scene %d=%+v", index, scene)
		}
	}

	start := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.security.studentToken, map[string]any{"plan_block_id": fixture.planBlockID})
	if start.Code != http.StatusOK {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	var session classroom.StudentSession
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.KnowledgePointID != activation.KnowledgePointID {
		t.Fatalf("classroom knowledge point=%s want the linear equation", session.KnowledgePointID)
	}
	// The reference answer of a choice question is one of its public options,
	// so only the private worked solution is checked as text here; private
	// answer keys are checked by assertStudentPayloadHasNoPrivateFields.
	var fullSolution string
	if err := pool.QueryRow(ctx, `SELECT full_solution_private FROM question_private_answers WHERE question_id=$1`, session.QuestionID).Scan(&fullSolution); err != nil {
		t.Fatal(err)
	}
	read := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+session.ID.String(), fixture.security.studentToken, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("read=%d %s", read.Code, read.Body.String())
	}
	for _, body := range [][]byte{start.Body.Bytes(), read.Body.Bytes()} {
		var payload classroom.StudentSession
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.WhyItMatters != linearEquationDailyLife {
			t.Fatalf("student why_it_matters=%q", payload.WhyItMatters)
		}
		assertStudentPayloadHasNoPrivateFields(t, body)
		for index, text := range []string{
			linearEquationHumanWorld, linearEquationFutureLearning, linearEquationCareerOrScience,
			linearEquationFoundation, linearEquationDifficultyPoints, linearEquationCommonStuckPoint,
			fullSolution,
		} {
			if text == "" {
				t.Fatalf("guarded text #%d is empty", index)
			}
			if strings.Contains(string(body), text) {
				t.Fatalf("student payload carries Owner-only life connection, breakdown or private answer text #%d", index)
			}
		}
		for _, key := range []string{"human_world", "future_learning", "career_or_science", "world_connections", "correct_answer_json"} {
			if strings.Contains(string(body), `"`+key+`"`) {
				t.Fatalf("student payload carries key %s", key)
			}
		}
	}
}
