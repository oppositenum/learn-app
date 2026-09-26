package api

import (
	"net/http"
	"strings"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/contentpipeline"
	"github.com/oppositenum/ai-learning-tutor/server/internal/health"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/internal/speech"
	"github.com/oppositenum/ai-learning-tutor/server/internal/student"
	"github.com/oppositenum/ai-learning-tutor/server/internal/trial"
)

type Middleware func(http.Handler) http.Handler

type Dependencies struct {
	Authenticate    Middleware
	PublicQuestions content.PublicQuestionReader
	Parents         *parent.Repository
	Realtime        *realtime.WebSocketHandler
	Classroom       *classroom.Handler
	Speech          *speech.Handler
	ContentPipeline *contentpipeline.Handler
	Identity        *auth.Handler
	Trial           *trial.Handler
}

func NewRouter(dependencies Dependencies) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Handler)
	if dependencies.Authenticate != nil && dependencies.Identity != nil {
		mux.HandleFunc("POST /api/v1/auth/login", dependencies.Identity.Login)
		mux.Handle("GET /api/v1/auth/me", dependencies.Authenticate(http.HandlerFunc(dependencies.Identity.Me)))
		mux.Handle("POST /api/v1/auth/logout", dependencies.Authenticate(http.HandlerFunc(dependencies.Identity.Logout)))
		owner := func(handler http.HandlerFunc) http.Handler {
			return dependencies.Authenticate(auth.RequireRole(auth.RoleOwner, handler))
		}
		mux.Handle("GET /api/v1/owner/accounts", owner(dependencies.Identity.OwnerAccounts))
		mux.Handle("POST /api/v1/owner/accounts/students", owner(dependencies.Identity.OwnerCreateStudent))
		mux.Handle("POST /api/v1/owner/accounts/parents", owner(dependencies.Identity.OwnerCreateParent))
		mux.Handle("POST /api/v1/owner/accounts/links", owner(dependencies.Identity.OwnerCreateParentLink))
	}

	if dependencies.Authenticate != nil && dependencies.PublicQuestions != nil {
		questions := student.NewQuestionHandler(dependencies.PublicQuestions)
		studentQuestion := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(questions.Get))
		mux.Handle("GET /api/v1/student/questions/{id}", dependencies.Authenticate(studentQuestion))
	}
	if dependencies.Authenticate != nil && dependencies.Speech != nil {
		transcribe := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Speech.Transcribe))
		mux.Handle("POST /api/v1/student/speech/transcriptions", dependencies.Authenticate(transcribe))
	}
	if dependencies.Authenticate != nil && dependencies.Parents != nil {
		live := parent.NewLiveHandler(dependencies.Parents)
		parentSession := auth.RequireRole(auth.RoleParent, http.HandlerFunc(live.GetSession))
		mux.Handle("GET /api/v1/parent/child/{student_id}/session/{session_id}", dependencies.Authenticate(parentSession))
	}
	if dependencies.Authenticate != nil && dependencies.Realtime != nil {
		studentSocket := auth.RequireRole(auth.RoleStudent, dependencies.Realtime)
		parentSocket := auth.RequireRole(auth.RoleParent, dependencies.Realtime)
		mux.Handle("GET /ws/student/{student_id}", dependencies.Authenticate(studentSocket))
		mux.Handle("GET /ws/parent/{student_id}", dependencies.Authenticate(parentSocket))
	}
	if dependencies.Authenticate != nil && dependencies.Classroom != nil {
		studentSubmit := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.SubmitAnswer))
		studentSupport := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.RequestSupport))
		studentVoiceReturn := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.ReturnFromVoice))
		studentBacktrackReturn := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.ReturnFromBacktrack))
		studentGrowth := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.Growth))
		studentToday := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.Today))
		studentStart := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.StartSession))
		studentSession := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.GetSession))
		studentCurrent := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.CurrentSession))
		studentPause := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.PauseSession))
		studentResume := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.ResumeSession))
		studentAbandon := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.AbandonSession))
		studentHeartbeat := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Classroom.HeartbeatSession))
		parentPreferences := auth.RequireRole(auth.RoleParent, http.HandlerFunc(dependencies.Classroom.ParentPreferences))
		parentPreferencesRead := auth.RequireRole(auth.RoleParent, http.HandlerFunc(dependencies.Classroom.GetParentPreferences))
		parentChildren := auth.RequireRole(auth.RoleParent, http.HandlerFunc(dependencies.Classroom.ParentChildren))
		parentAbility := auth.RequireRole(auth.RoleParent, http.HandlerFunc(dependencies.Classroom.ParentAbility))
		parentReport := auth.RequireRole(auth.RoleParent, http.HandlerFunc(dependencies.Classroom.ParentReport))
		parentSafety := auth.RequireRole(auth.RoleParent, http.HandlerFunc(dependencies.Classroom.ParentSafetyEvents))
		parentIntervention := auth.RequireRole(auth.RoleParent, http.HandlerFunc(dependencies.Classroom.ParentIntervention))
		ownerCosts := auth.RequireRole(auth.RoleOwner, http.HandlerFunc(dependencies.Classroom.OwnerCosts))
		ownerLearningEffects := auth.RequireRole(auth.RoleOwner, http.HandlerFunc(dependencies.Classroom.OwnerLearningEffects))
		ownerContent := auth.RequireRole(auth.RoleOwner, http.HandlerFunc(dependencies.Classroom.OwnerContent))
		mux.Handle("POST /api/v1/student/sessions/{session_id}/answers", dependencies.Authenticate(studentSubmit))
		mux.Handle("POST /api/v1/student/sessions/{session_id}/support", dependencies.Authenticate(studentSupport))
		mux.Handle("POST /api/v1/student/sessions/{session_id}/voice/complete", dependencies.Authenticate(studentVoiceReturn))
		mux.Handle("POST /api/v1/student/sessions/{session_id}/backtrack/return", dependencies.Authenticate(studentBacktrackReturn))
		mux.Handle("GET /api/v1/student/growth", dependencies.Authenticate(studentGrowth))
		mux.Handle("GET /api/v1/student/today", dependencies.Authenticate(studentToday))
		mux.Handle("POST /api/v1/student/sessions", dependencies.Authenticate(studentStart))
		mux.Handle("GET /api/v1/student/sessions/current", dependencies.Authenticate(studentCurrent))
		mux.Handle("GET /api/v1/student/sessions/{session_id}", dependencies.Authenticate(studentSession))
		mux.Handle("POST /api/v1/student/sessions/{session_id}/pause", dependencies.Authenticate(studentPause))
		mux.Handle("POST /api/v1/student/sessions/{session_id}/resume", dependencies.Authenticate(studentResume))
		mux.Handle("POST /api/v1/student/sessions/{session_id}/abandon", dependencies.Authenticate(studentAbandon))
		mux.Handle("POST /api/v1/student/sessions/{session_id}/heartbeat", dependencies.Authenticate(studentHeartbeat))
		mux.Handle("PUT /api/v1/parent/child/{student_id}/preferences", dependencies.Authenticate(parentPreferences))
		mux.Handle("GET /api/v1/parent/child/{student_id}/preferences", dependencies.Authenticate(parentPreferencesRead))
		mux.Handle("GET /api/v1/parent/children", dependencies.Authenticate(parentChildren))
		mux.Handle("GET /api/v1/parent/child/{student_id}/ability", dependencies.Authenticate(parentAbility))
		mux.Handle("GET /api/v1/parent/child/{student_id}/report", dependencies.Authenticate(parentReport))
		mux.Handle("GET /api/v1/parent/child/{student_id}/safety-events", dependencies.Authenticate(parentSafety))
		mux.Handle("POST /api/v1/parent/child/{student_id}/interventions", dependencies.Authenticate(parentIntervention))
		mux.Handle("GET /api/v1/owner/costs", dependencies.Authenticate(ownerCosts))
		mux.Handle("GET /api/v1/owner/learning-effects", dependencies.Authenticate(ownerLearningEffects))
		mux.Handle("GET /api/v1/owner/content", dependencies.Authenticate(ownerContent))
	}
	if dependencies.Authenticate != nil && dependencies.ContentPipeline != nil {
		owner := func(handler http.HandlerFunc) http.Handler {
			return dependencies.Authenticate(auth.RequireRole(auth.RoleOwner, handler))
		}
		mux.Handle("GET /api/v1/owner/content/generation-options", owner(dependencies.ContentPipeline.GenerationOptions))
		mux.Handle("POST /api/v1/owner/content/generate", owner(dependencies.ContentPipeline.Generate))
		mux.Handle("POST /api/v1/owner/content/drafts", owner(dependencies.ContentPipeline.ImportDraft))
		mux.Handle("POST /api/v1/owner/content/{question_id}/validate", owner(dependencies.ContentPipeline.Validate))
		mux.Handle("POST /api/v1/owner/content/{question_id}/review", owner(dependencies.ContentPipeline.Review))
		mux.Handle("POST /api/v1/owner/content/{question_id}/release", owner(dependencies.ContentPipeline.Release))
		mux.Handle("POST /api/v1/owner/content/{question_id}/quarantine", owner(dependencies.ContentPipeline.Quarantine))
	}
	if dependencies.Authenticate != nil && dependencies.Trial != nil {
		studentReflection := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(dependencies.Trial.SubmitReflection))
		ownerTrialReport := auth.RequireRole(auth.RoleOwner, http.HandlerFunc(dependencies.Trial.OwnerReport))
		mux.Handle("POST /api/v1/student/sessions/{session_id}/reflection", dependencies.Authenticate(studentReflection))
		mux.Handle("GET /api/v1/owner/trials", dependencies.Authenticate(ownerTrialReport))
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") {
			writer.Header().Set("Cache-Control", "no-store")
		}
		mux.ServeHTTP(writer, request)
	})
}
