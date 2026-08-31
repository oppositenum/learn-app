package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestParentAbilityAndReportAreBoundReadModelsWithoutPrivateAnswers(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	parents := parent.NewRepository(pool)
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(classroom.NewService(pool, nil, nil, nil), pool, parents),
	})

	ability := performJSON(router, http.MethodGet, "/api/v1/parent/child/"+fixture.studentID.String()+"/ability", fixture.parentToken, nil)
	if ability.Code != http.StatusOK || !strings.Contains(ability.Body.String(), `"subjects"`) || !strings.Contains(ability.Body.String(), `"core_abilities"`) {
		t.Fatalf("ability=%d %s", ability.Code, ability.Body.String())
	}
	report := performJSON(router, http.MethodGet, "/api/v1/parent/child/"+fixture.studentID.String()+"/report", fixture.parentToken, nil)
	if report.Code != http.StatusOK || !strings.Contains(report.Body.String(), `"completed_sessions"`) || !strings.Contains(report.Body.String(), fixture.sessionID.String()) {
		t.Fatalf("report=%d %s", report.Code, report.Body.String())
	}
	for name, payload := range map[string]string{"ability": ability.Body.String(), "report": report.Body.String()} {
		if strings.Contains(payload, fixture.privateCanary) || strings.Contains(strings.ToLower(payload), "correct_answer") || strings.Contains(strings.ToLower(payload), "full_solution") {
			t.Fatalf("%s leaked private answer data: %s", name, payload)
		}
	}
	studentAttempt := performJSON(router, http.MethodGet, "/api/v1/parent/child/"+fixture.studentID.String()+"/ability", fixture.studentToken, nil)
	if studentAttempt.Code != http.StatusForbidden {
		t.Fatalf("Student accessed Parent ability read model: %d", studentAttempt.Code)
	}
}
