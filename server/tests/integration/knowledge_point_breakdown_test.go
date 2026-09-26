package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

// The §1.1 breakdown written for MATH-LINEAR-EQUATION in the product document.
const (
	linearEquationFoundation       = "四则运算熟练、负数概念、等式性质"
	linearEquationDifficultyPoints = "把文字问题翻译成方程；等式两边同时运算；检验答案"
	linearEquationCommonStuckPoint = "不会算不是主要问题，而是看不到题目里的等量关系，所以不知道为什么要列方程"
)

func TestKnowledgePointBreakdownIsWrittenOnlyForLinearEquation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	var foundation, difficultyPoints, stuckPoint *string
	if err := pool.QueryRow(ctx, `SELECT foundation,difficulty_points,common_stuck_point FROM knowledge_points WHERE code='MATH-LINEAR-EQUATION'`).Scan(&foundation, &difficultyPoints, &stuckPoint); err != nil {
		t.Fatal(err)
	}
	if foundation == nil || *foundation != linearEquationFoundation {
		t.Fatalf("foundation=%v", foundation)
	}
	if difficultyPoints == nil || *difficultyPoints != linearEquationDifficultyPoints {
		t.Fatalf("difficulty_points=%v", difficultyPoints)
	}
	if stuckPoint == nil || *stuckPoint != linearEquationCommonStuckPoint {
		t.Fatalf("common_stuck_point=%v", stuckPoint)
	}
	var others, filled int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE foundation IS NOT NULL OR difficulty_points IS NOT NULL OR common_stuck_point IS NOT NULL) FROM knowledge_points WHERE code<>'MATH-LINEAR-EQUATION'`).Scan(&others, &filled); err != nil {
		t.Fatal(err)
	}
	if others == 0 || filled != 0 {
		t.Fatalf("other knowledge points=%d with a breakdown=%d", others, filled)
	}
}

type ownerContentBreakdown struct {
	KnowledgePointCode string `json:"knowledge_point_code"`
	Foundation         string `json:"foundation"`
	DifficultyPoints   string `json:"difficulty_points"`
	CommonStuckPoint   string `json:"common_stuck_point"`
	ReleaseRecords     []struct {
		FromStatus string `json:"from_status"`
		ToStatus   string `json:"to_status"`
		At         string `json:"at"`
	} `json:"release_records"`
}

// The Owner content page sees the breakdown and the release history of the
// knowledge point; the Student classroom on the same knowledge point sees
// neither the fields nor their text.
func TestOwnerContentShowsLinearEquationBreakdownButStudentDoesNot(t *testing.T) {
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
	for _, detail := range []string{"findings_json", "checks_json"} {
		if strings.Contains(response.Body.String(), detail) {
			t.Fatalf("owner content spread %s", detail)
		}
	}
	var report struct {
		KnowledgePoints []ownerContentBreakdown `json:"knowledge_points"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.KnowledgePoints) != 1 {
		t.Fatalf("owner content knowledge points=%d want only MATH-LINEAR-EQUATION", len(report.KnowledgePoints))
	}
	point := report.KnowledgePoints[0]
	if point.KnowledgePointCode != "MATH-LINEAR-EQUATION" || point.Foundation != linearEquationFoundation || point.DifficultyPoints != linearEquationDifficultyPoints || point.CommonStuckPoint != linearEquationCommonStuckPoint {
		t.Fatalf("owner breakdown=%+v", point)
	}
	var releases, releasedTransitions int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE r.to_status='RELEASED') FROM content_release_records r JOIN questions q ON q.id=r.question_id WHERE q.knowledge_point_id=$1`, activation.KnowledgePointID).Scan(&releases, &releasedTransitions); err != nil {
		t.Fatal(err)
	}
	if releasedTransitions == 0 || len(point.ReleaseRecords) != min(releases, 100) {
		t.Fatalf("owner release records=%d database=%d released=%d", len(point.ReleaseRecords), releases, releasedTransitions)
	}
	sawReleased := false
	for _, release := range point.ReleaseRecords {
		if release.FromStatus == "" || release.ToStatus == "" || release.At == "" {
			t.Fatalf("incomplete release record %+v", release)
		}
		sawReleased = sawReleased || release.ToStatus == "RELEASED"
	}
	if !sawReleased {
		t.Fatal("owner release records omit the RELEASED transition")
	}

	start := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.security.studentToken, map[string]any{"plan_block_id": fixture.planBlockID})
	if start.Code != http.StatusOK {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	var session classroom.StudentSession
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	read := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+session.ID.String(), fixture.security.studentToken, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("read=%d %s", read.Code, read.Body.String())
	}
	for _, body := range [][]byte{start.Body.Bytes(), read.Body.Bytes()} {
		assertStudentPayloadHasNoPrivateFields(t, body)
		for _, text := range []string{linearEquationFoundation, linearEquationDifficultyPoints, linearEquationCommonStuckPoint} {
			if strings.Contains(string(body), text) {
				t.Fatalf("student payload carries the knowledge point breakdown")
			}
		}
	}
}
