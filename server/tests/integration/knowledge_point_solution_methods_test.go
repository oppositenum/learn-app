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

// solutionMethod is one method answered through the five questions.
type solutionMethod struct {
	Sequence      int    `json:"sequence"`
	MethodName    string `json:"method_name"`
	FirstLook     string `json:"first_look"`
	WhyThisMethod string `json:"why_this_method"`
	MethodPath    string `json:"method_path"`
	CheckWhere    string `json:"check_where"`
	MoreDirect    string `json:"more_direct"`
}

func (method solutionMethod) sentences() []string {
	return []string{method.FirstLook, method.WhyThisMethod, method.MethodPath, method.CheckWhere, method.MoreDirect}
}

// The two §6.1 strategies written for MATH-LINEAR-EQUATION.
var linearEquationSolutionMethods = []solutionMethod{
	{
		Sequence:      1,
		MethodName:    "假设法",
		FirstLook:     "先看一共有多少个头、一共有多少只脚。",
		WhyThisMethod: "先当成全是鸡，脚的差额就知道兔子有几只，不用一开始列两个未知数。",
		MethodPath:    "全假设，算差额，用每只多出来的脚数去除，再得到另一种。",
		CheckWhere:    "用两种数量分别乘脚数，加起来是否等于总脚数。",
		MoreDirect:    "熟练以后可以直接列方程。",
	},
	{
		Sequence:      2,
		MethodName:    "列方程法",
		FirstLook:     "先看哪个量不知道，哪个量和它按固定关系一起变。",
		WhyThisMethod: "关系已经是一次的，设未知数比反复试数更清楚。",
		MethodPath:    "设未知数，写固定量加每份变化量，让它等于总量，再解。",
		CheckWhere:    "把求出的数代回原式，看等号两边是否相同。",
		MoreDirect:    "数字很小的时候也可以画图或列表。",
	},
}

func TestSolutionMethodsAreWrittenOnlyForLinearEquation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `
SELECT m.sequence,m.method_name,m.first_look,m.why_this_method,m.method_path,m.check_where,m.more_direct
FROM knowledge_point_solution_methods m JOIN knowledge_points kp ON kp.id=m.knowledge_point_id
WHERE kp.code='MATH-LINEAR-EQUATION' ORDER BY m.sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	methods := []solutionMethod{}
	for rows.Next() {
		var method solutionMethod
		if err := rows.Scan(&method.Sequence, &method.MethodName, &method.FirstLook, &method.WhyThisMethod, &method.MethodPath, &method.CheckWhere, &method.MoreDirect); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, method)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(methods) != len(linearEquationSolutionMethods) {
		t.Fatalf("linear equation solution methods=%d want %d", len(methods), len(linearEquationSolutionMethods))
	}
	for index, method := range methods {
		if method != linearEquationSolutionMethods[index] {
			t.Fatalf("solution method %d=%+v want %+v", index, method, linearEquationSolutionMethods[index])
		}
	}
	var others, otherMethods int
	if err := pool.QueryRow(ctx, `
SELECT count(*),COALESCE(sum((SELECT count(*) FROM knowledge_point_solution_methods m WHERE m.knowledge_point_id=kp.id)),0)
FROM knowledge_points kp WHERE kp.code<>'MATH-LINEAR-EQUATION'`).Scan(&others, &otherMethods); err != nil {
		t.Fatal(err)
	}
	if others == 0 || otherMethods != 0 {
		t.Fatalf("other knowledge points=%d with solution methods=%d", others, otherMethods)
	}
}

// The Owner content page lists both methods; the Student classroom on the same
// knowledge point carries neither the fields nor any of the ten sentences.
func TestOwnerContentShowsSolutionMethodsButStudentDoesNot(t *testing.T) {
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
			KnowledgePointCode string           `json:"knowledge_point_code"`
			SolutionMethods    []solutionMethod `json:"solution_methods"`
		} `json:"knowledge_points"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.KnowledgePoints) != 1 || report.KnowledgePoints[0].KnowledgePointCode != "MATH-LINEAR-EQUATION" {
		t.Fatalf("owner content knowledge points=%d", len(report.KnowledgePoints))
	}
	owned := report.KnowledgePoints[0].SolutionMethods
	if len(owned) != len(linearEquationSolutionMethods) {
		t.Fatalf("owner solution methods=%d", len(owned))
	}
	for index, method := range owned {
		if method != linearEquationSolutionMethods[index] {
			t.Fatalf("owner solution method %d=%+v", index, method)
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
	read := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+session.ID.String(), fixture.security.studentToken, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("read=%d %s", read.Code, read.Body.String())
	}
	for _, body := range [][]byte{start.Body.Bytes(), read.Body.Bytes()} {
		assertStudentPayloadHasNoPrivateFields(t, body)
		for _, method := range linearEquationSolutionMethods {
			for index, sentence := range method.sentences() {
				if strings.Contains(string(body), sentence) {
					t.Fatalf("student payload carries sentence %d of %s", index+1, method.MethodName)
				}
			}
		}
	}
}
