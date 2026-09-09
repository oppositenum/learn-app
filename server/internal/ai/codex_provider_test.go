package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

type structuredClientStub struct {
	result      StructuredResult
	err         error
	results     []StructuredResult
	errors      []error
	request     StructuredRequest
	requests    []StructuredRequest
	calls       int
	deadline    time.Time
	hasDeadline bool
}

type tutorOutputAuditorStub struct {
	requests    []TutorOutputAuditRequest
	err         error
	deadline    time.Time
	hasDeadline bool
}

func (stub *tutorOutputAuditorStub) AuditTutorOutput(ctx context.Context, request TutorOutputAuditRequest) error {
	stub.requests = append(stub.requests, request)
	stub.deadline, stub.hasDeadline = ctx.Deadline()
	return stub.err
}

func passingTutorOutputAuditor() *tutorOutputAuditorStub { return &tutorOutputAuditorStub{} }

func (stub *structuredClientStub) GenerateStructured(ctx context.Context, request StructuredRequest) (StructuredResult, error) {
	index := stub.calls
	stub.request = request
	stub.requests = append(stub.requests, request)
	stub.calls++
	stub.deadline, stub.hasDeadline = ctx.Deadline()
	if index < len(stub.errors) && stub.errors[index] != nil {
		return StructuredResult{}, stub.errors[index]
	}
	if index < len(stub.results) {
		return stub.results[index], nil
	}
	return stub.result, stub.err
}

func validGeneratedTurn(action tutor.State) StructuredResult {
	return StructuredResult{
		ResponseID: "response-success",
		OutputJSON: json.RawMessage(`{"message":"请先观察题目中的关系。","action":"` + string(action) + `","answer_revealed":false,"segments":[]}`),
	}
}

func validAnswerAnalysis() StructuredResult {
	return StructuredResult{OutputJSON: json.RawMessage(`{
		"answer_correct":false,
		"reasoning_quality":"WEAK",
		"confidence":0.8,
		"error_type":"NEEDS_MORE_REASONING",
		"misconceptions":[],
		"core_ability_signals":[],
		"emotion_signal":"NEUTRAL",
		"engagement":"NORMAL",
		"recommended_action":"PROBE",
		"safe_to_increase_difficulty":false
	}`)}
}

func configureImmediateGenerationRetries(provider *CodexProvider, waits *[]time.Duration) {
	provider.generationRetryJitter = func(time.Duration) time.Duration { return 0 }
	provider.waitForGenerationRetry = func(_ context.Context, delay time.Duration) error {
		*waits = append(*waits, delay)
		return nil
	}
}

func generationResponseError(status int, retryAfter time.Duration, retryAfterSet bool) error {
	return &ResponsesAPIError{
		StatusCode: status, RetryAfter: retryAfter, RetryAfterSet: retryAfterSet,
		providerCode: "gateway_concurrency_limit",
	}
}

func TestCodexProviderAnalysis429RetryRecoversWithSharedPolicy(t *testing.T) {
	client := &structuredClientStub{
		errors:  []error{generationResponseError(http.StatusTooManyRequests, 0, false), nil},
		results: []StructuredResult{{}, validAnswerAnalysis()},
	}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	configureImmediateGenerationRetries(provider, &waits)

	if _, err := provider.AnalyzeAnswer(context.Background(), AnalyzeAnswerRequest{}); err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 || !slices.Equal(waits, []time.Duration{TutorRetryBaseDelay}) {
		t.Fatalf("analysis calls=%d waits=%v", client.calls, waits)
	}
	if len(client.requests) != 2 || client.requests[0].RequestID == "" || client.requests[0].RequestID == client.requests[1].RequestID {
		t.Fatalf("analysis request IDs=%q %q", client.requests[0].RequestID, client.requests[1].RequestID)
	}
	if client.requests[0].Purpose != PurposeAnswerAnalysis || client.requests[1].Purpose != PurposeAnswerAnalysis {
		t.Fatalf("analysis purposes=%q/%q", client.requests[0].Purpose, client.requests[1].Purpose)
	}
	t.Logf("analysis 429 recovery calls=%d waits=%v", client.calls, waits)
}

func TestCodexProviderAnalysis5xxUsesSharedRetryPredicate(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := &structuredClientStub{
				errors:  []error{generationResponseError(status, 0, false), nil},
				results: []StructuredResult{{}, validAnswerAnalysis()},
			}
			provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
			if err != nil {
				t.Fatal(err)
			}
			var waits []time.Duration
			configureImmediateGenerationRetries(provider, &waits)

			if _, err := provider.AnalyzeAnswer(context.Background(), AnalyzeAnswerRequest{}); err != nil {
				t.Fatal(err)
			}
			if client.calls != 2 || !slices.Equal(waits, []time.Duration{TutorRetryBaseDelay}) {
				t.Fatalf("status=%d analysis calls=%d waits=%v", status, client.calls, waits)
			}
			t.Logf("status=%d analysis calls=%d waits=%v", status, client.calls, waits)
		})
	}
}

func TestCodexProviderAnalysis429RetryExhaustionUsesExistingBusyError(t *testing.T) {
	client := &structuredClientStub{errors: []error{
		generationResponseError(http.StatusTooManyRequests, 0, false),
		generationResponseError(http.StatusTooManyRequests, 0, false),
		generationResponseError(http.StatusTooManyRequests, 0, false),
	}}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	configureImmediateGenerationRetries(provider, &waits)

	_, err = provider.AnalyzeAnswer(context.Background(), AnalyzeAnswerRequest{})
	if !errors.Is(err, ErrTutorGenerationBusy) || !IsRetryableResponsesError(err) {
		t.Fatalf("analysis exhaustion error=%v", err)
	}
	details, ok := TutorGenerationBusyFailureDetails(err)
	if !ok || details.Category != TutorReviewFailureRetryExhausted || details.HTTPStatus != http.StatusTooManyRequests ||
		details.ProviderCode != "gateway_concurrency_limit" || details.RequestID != client.requests[2].RequestID {
		t.Fatalf("analysis exhaustion details=%+v ok=%v", details, ok)
	}
	if err.Error() != ErrTutorGenerationBusy.Error() {
		t.Fatalf("analysis error exposed provider details: %q", err)
	}
	if client.calls != TutorRetryMaxAttempts || !slices.Equal(waits, []time.Duration{TutorRetryBaseDelay, 2 * TutorRetryBaseDelay}) {
		t.Fatalf("analysis calls=%d waits=%v", client.calls, waits)
	}
	t.Logf("analysis 429 exhaustion calls=%d waits=%v", client.calls, waits)
}

func TestCodexProviderAnalysis400DoesNotRetry(t *testing.T) {
	client := &structuredClientStub{errors: []error{generationResponseError(http.StatusBadRequest, 0, false)}}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	configureImmediateGenerationRetries(provider, &waits)

	_, err = provider.AnalyzeAnswer(context.Background(), AnalyzeAnswerRequest{})
	if err == nil || errors.Is(err, ErrTutorGenerationBusy) {
		t.Fatalf("400 analysis error=%v", err)
	}
	if client.calls != 1 || len(waits) != 0 {
		t.Fatalf("400 analysis calls=%d waits=%v", client.calls, waits)
	}
	t.Logf("analysis 400 calls=%d waits=%v", client.calls, waits)
}

func TestCodexProviderAnalysisRetryHonorsRetryAfter(t *testing.T) {
	client := &structuredClientStub{
		errors:  []error{generationResponseError(http.StatusTooManyRequests, 7*time.Second, true), nil},
		results: []StructuredResult{{}, validAnswerAnalysis()},
	}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	configureImmediateGenerationRetries(provider, &waits)

	if _, err := provider.AnalyzeAnswer(context.Background(), AnalyzeAnswerRequest{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(waits, []time.Duration{7 * time.Second}) {
		t.Fatalf("analysis Retry-After waits=%v", waits)
	}
}

func TestCodexProviderRejectsSchemaViolation(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
        "message":"先看固定费用。",
        "action":"PROBE",
        "answer_revealed":false,
        "segments":[],
        "unexpected":"must fail"
    }`)}}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	_, err = provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		TutorDecision: tutor.Decision{NextState: tutor.StateProbe},
	})
	if err == nil {
		t.Fatal("expected additional property to violate strict schema")
	}
}

func TestCodexProviderDoesNotTrustAnswerRevealedWhenAuditorRejectsBody(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
        "message":"原题答案是10。",
        "action":"EXPLAIN",
		"answer_revealed":false,
        "segments":[]
    }`)}}
	auditErr := errors.New("deterministic answer match")
	auditor := &tutorOutputAuditorStub{err: auditErr}
	provider, err := NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	_, err = provider.GenerateExplanation(context.Background(), ExplainRequest{
		TutorDecision: tutor.Decision{NextState: tutor.StateExplain, AnswerRevealAllowed: false},
	})
	if !errors.Is(err, auditErr) || len(auditor.requests) != 1 || auditor.requests[0].Candidate.Message != "原题答案是10。" {
		t.Fatalf("auditor did not reject self-reported-safe answer: err=%v requests=%+v", err, auditor.requests)
	}
}

func TestCodexProviderRejectsIntroducedNumbersAfterIndependentDisclosureAudit(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
		"message":"如果改成每盒10支会怎样？","action":"HINT","answer_revealed":false,"segments":[]
	}`)}}
	auditor := passingTutorOutputAuditor()
	provider, err := NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		Question:      content.QuestionPublic{Prompt: "每盒12支，共3盒。"},
		TutorDecision: tutor.Decision{NextState: tutor.StateHint},
	})
	if !errors.Is(err, ErrTutorOutputRephraseRequired) || !errors.Is(err, ErrTutorMaterialPolicyViolation) {
		t.Fatalf("error=%v", err)
	}
	if len(auditor.requests) != 1 {
		t.Fatalf("existing independent disclosure audit was bypassed: %+v", auditor.requests)
	}
}

func TestCodexProviderAddsOriginalTaskVerificationAfterExplanationAudit(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
		"message":"先看一个不同数字的平行例子。","action":"EXPLAIN","answer_revealed":false,"segments":[]
	}`)}}
	auditor := passingTutorOutputAuditor()
	provider, err := NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := provider.GenerateParallelExample(context.Background(), ExampleRequest(GenerateTurnRequest{
		Question:      content.QuestionPublic{Prompt: "每盒12支，共3盒。"},
		TutorDecision: tutor.Decision{NextState: tutor.StateExplain},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(auditor.requests) != 1 || strings.Contains(auditor.requests[0].Candidate.Message, "回到原题") {
		t.Fatalf("reviewer did not receive original provider output: %+v", auditor.requests)
	}
	if !strings.Contains(turn.Message, "现在回到原题，请你再独立试一次。") {
		t.Fatalf("student output lacks original-task verification: %q", turn.Message)
	}
}

func TestCodexProviderFailsClosedWithoutTutorOutputAuditor(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
		"message":"先看数量关系。","action":"PROBE","answer_revealed":false,"segments":[]
	}`)}}
	provider, err := NewCodexProvider(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{TutorDecision: tutor.Decision{NextState: tutor.StateProbe}}); !errors.Is(err, ErrTutorOutputAuditorUnavailable) {
		t.Fatalf("missing auditor did not fail closed: %v", err)
	}
	if client.request.SchemaName != "" {
		t.Fatal("Tutor provider was called before the missing-auditor failure")
	}
}

func TestCodexProviderCarriesResponseContext(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{
		ResponseID: "resp_next",
		OutputJSON: json.RawMessage(`{
            "message":"配送费是哪一部分？",
            "action":"PROBE",
            "answer_revealed":false,
            "segments":[]
        }`),
	}}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	turn, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		PreviousResponseID: "resp_previous",
		TutorDecision:      tutor.Decision{NextState: tutor.StateProbe},
	})
	if err != nil {
		t.Fatalf("generate turn: %v", err)
	}
	if client.request.PreviousResponseID != "resp_previous" || turn.ResponseID != "resp_next" {
		t.Fatalf("response context was not preserved: request=%q result=%q", client.request.PreviousResponseID, turn.ResponseID)
	}
	if len(client.request.Schema) == 0 {
		t.Fatal("provider did not send a strict output schema")
	}
}

func TestCodexProviderConstrainsActionEnumToServerDecision(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
        "message":"给你一个方向。",
        "action":"HINT",
        "answer_revealed":false,
        "segments":[]
    }`)}}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		TutorDecision: tutor.Decision{NextState: tutor.StateHint},
	}); err != nil {
		t.Fatalf("generate turn: %v", err)
	}
	var outgoing map[string]any
	if err := json.Unmarshal(client.request.Schema, &outgoing); err != nil {
		t.Fatalf("decode outgoing schema: %v", err)
	}
	action := outgoing["properties"].(map[string]any)["action"].(map[string]any)
	enum, ok := action["enum"].([]any)
	if !ok || len(enum) != 1 || enum[0] != "HINT" {
		t.Fatalf("outgoing action enum was not constrained to the server decision: %v", action["enum"])
	}
}

func TestCodexProviderSendsOnlyPublicQuestionDataToTutorGeneration(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
        "message":"先观察题目中的数量关系。",
        "action":"PROBE",
        "answer_revealed":false,
        "segments":[]
    }`)}}
	auditor := passingTutorOutputAuditor()
	provider, err := NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	_, err = provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		Question: content.QuestionPublic{
			Prompt:      "公开题面",
			Scene:       json.RawMessage(`{"kind":"NUMBER_LINE"}`),
			InputSchema: json.RawMessage(`{"type":"string"}`),
		},
		AuditPrivateAnswer: content.QuestionPrivateAnswer{
			CorrectAnswer: json.RawMessage(`{"value":"private_answer_canary"}`),
			FullSolution:  "private_full_solution_canary", TeacherReferenceAnswer: "private_teacher_reference_canary",
		},
		TutorDecision: tutor.Decision{NextState: tutor.StateProbe},
	})
	if err != nil {
		t.Fatalf("generate turn: %v", err)
	}
	payload := strings.ToLower(string(client.request.Input))
	if !strings.Contains(payload, "公开题面") {
		t.Fatalf("public prompt missing from teaching request: %s", client.request.Input)
	}
	for _, forbidden := range []string{
		"correct_answer",
		"full_solution",
		"teacher_reference_answer",
		"scoring_key",
		"misconceptions",
		"hint_policy",
		"private_answer_canary",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("teaching request contains private answer field %q: %s", forbidden, client.request.Input)
		}
	}
	if len(auditor.requests) != 1 || !strings.Contains(string(auditor.requests[0].PrivateAnswer.CorrectAnswer), "private_answer_canary") {
		t.Fatalf("auditor did not receive private server context: %+v", auditor.requests)
	}
}

func TestCodexProviderAuditsEveryStudentVisibleGenerationEntryPoint(t *testing.T) {
	for _, test := range []struct {
		name   string
		action tutor.State
		call   func(*CodexProvider, GenerateTurnRequest) error
	}{
		{name: "turn", action: tutor.StateHint, call: func(provider *CodexProvider, request GenerateTurnRequest) error {
			_, err := provider.GenerateTurn(context.Background(), request)
			return err
		}},
		{name: "analogy", action: tutor.StateAnalogy, call: func(provider *CodexProvider, request GenerateTurnRequest) error {
			_, err := provider.GenerateAnalogy(context.Background(), AnalogyRequest(request))
			return err
		}},
		{name: "parallel example", action: tutor.StateExplain, call: func(provider *CodexProvider, request GenerateTurnRequest) error {
			_, err := provider.GenerateParallelExample(context.Background(), ExampleRequest(request))
			return err
		}},
		{name: "explanation", action: tutor.StateVoiceExplain, call: func(provider *CodexProvider, request GenerateTurnRequest) error {
			_, err := provider.GenerateExplanation(context.Background(), ExplainRequest(request))
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &structuredClientStub{result: StructuredResult{ResponseID: "resp-audited", OutputJSON: json.RawMessage(`{
				"message":"先找题目条件。","action":"` + string(test.action) + `","answer_revealed":false,
				"segments":[{"id":"s1","text":"先找条件。"}]
			}`)}}
			auditor := passingTutorOutputAuditor()
			provider, err := NewCodexProvider(client, auditor)
			if err != nil {
				t.Fatal(err)
			}
			request := GenerateTurnRequest{StudentID: "student", SessionID: "session", TutorDecision: tutor.Decision{NextState: test.action}}
			if err := test.call(provider, request); err != nil {
				t.Fatal(err)
			}
			if len(auditor.requests) != 1 || len(auditor.requests[0].Candidate.Segments) != 1 || auditor.requests[0].GeneratorResponseID != "resp-audited" {
				t.Fatalf("entry point did not cross audit gate: %+v", auditor.requests)
			}
			for _, required := range []string{"Never directly quote, repeat, or equivalently paraphrase", "Do not locate evidence for the student", "Never provide the answer or a complete solution"} {
				if !strings.Contains(client.request.Instructions, required) {
					t.Fatalf("%s instructions missing shared disclosure constraint %q: %s", test.name, required, client.request.Instructions)
				}
			}
			if test.action == tutor.StateHint {
				if !strings.Contains(client.request.Instructions, hintInstructions) {
					t.Fatalf("HINT instructions missing dedicated constraint: %s", client.request.Instructions)
				}
			} else if strings.Contains(client.request.Instructions, hintInstructions) {
				t.Fatalf("%s unexpectedly received HINT-only instructions: %s", test.name, client.request.Instructions)
			}
			if test.name == "parallel example" {
				const unchanged = "Explain with a parallel example using different values. Do not solve the original question."
				if !strings.HasPrefix(client.request.Instructions, unchanged+" ") {
					t.Fatalf("parallel-example instruction changed: %s", client.request.Instructions)
				}
			}
		})
	}
}

func TestCodexProviderBoundsGenerationRetryInsideTutorOperationDeadline(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
		"message":"先找题目条件。","action":"HINT","answer_revealed":false,"segments":[]
	}`)}}
	auditor := passingTutorOutputAuditor()
	provider, err := NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		TutorDecision: tutor.Decision{NextState: tutor.StateHint},
	}); err != nil {
		t.Fatal(err)
	}
	remaining := client.deadline.Sub(started)
	if !client.hasDeadline || !auditor.hasDeadline || !client.deadline.Before(auditor.deadline) {
		t.Fatalf("generation deadline=%v/%v audit deadline=%v/%v", client.deadline, client.hasDeadline, auditor.deadline, auditor.hasDeadline)
	}
	if remaining <= TutorRetryOverallTimeout-time.Second || remaining > TutorRetryOverallTimeout+100*time.Millisecond {
		t.Fatalf("generation retry deadline remaining=%v", remaining)
	}
	auditRemaining := auditor.deadline.Sub(started)
	if auditRemaining <= tutorOutputOperationTimeout-time.Second || auditRemaining > tutorOutputOperationTimeout+100*time.Millisecond {
		t.Fatalf("operation deadline remaining=%v", auditRemaining)
	}
	if client.calls != 1 {
		t.Fatalf("Tutor generation calls=%d want=1", client.calls)
	}
}

func TestCodexProviderGenerationRetryUsesSharedPolicy(t *testing.T) {
	provider, err := NewCodexProvider(&structuredClientStub{}, passingTutorOutputAuditor())
	if err != nil {
		t.Fatal(err)
	}
	policy := provider.generationRetry
	if policy.maxAttempts != TutorRetryMaxAttempts || policy.baseDelay != TutorRetryBaseDelay ||
		policy.maxJitter != TutorRetryMaxJitter || policy.maxRetryWait != TutorRetryMaxWait ||
		policy.overallTimeout != TutorRetryOverallTimeout {
		t.Fatalf("generation retry policy=%+v", policy)
	}
	t.Logf("generation retry max_attempts=%d base_delay=%s max_jitter=%s max_wait=%s overall_timeout=%s",
		policy.maxAttempts, policy.baseDelay, policy.maxJitter, policy.maxRetryWait, policy.overallTimeout)
}

func TestCodexProviderGeneration429RetryRecoversBeforeOneAudit(t *testing.T) {
	client := &structuredClientStub{
		errors:  []error{generationResponseError(http.StatusTooManyRequests, 0, false), nil},
		results: []StructuredResult{{}, validGeneratedTurn(tutor.StateHint)},
	}
	auditor := passingTutorOutputAuditor()
	provider, err := NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	configureImmediateGenerationRetries(provider, &waits)

	if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{TutorDecision: tutor.Decision{NextState: tutor.StateHint}}); err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 || len(auditor.requests) != 1 || !slices.Equal(waits, []time.Duration{2 * time.Second}) {
		t.Fatalf("generation calls=%d audits=%d waits=%v", client.calls, len(auditor.requests), waits)
	}
	if len(client.requests) != 2 || client.requests[0].RequestID == "" || client.requests[0].RequestID == client.requests[1].RequestID {
		t.Fatalf("generation request IDs=%q %q", client.requests[0].RequestID, client.requests[1].RequestID)
	}
	t.Logf("generation 429 recovery calls=%d audits=%d waits=%v", client.calls, len(auditor.requests), waits)
}

func TestCodexProviderGeneration5xxUsesSharedRetryPredicate(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := &structuredClientStub{
				errors:  []error{generationResponseError(status, 0, false), nil},
				results: []StructuredResult{{}, validGeneratedTurn(tutor.StateHint)},
			}
			auditor := passingTutorOutputAuditor()
			provider, err := NewCodexProvider(client, auditor)
			if err != nil {
				t.Fatal(err)
			}
			var waits []time.Duration
			configureImmediateGenerationRetries(provider, &waits)

			if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{TutorDecision: tutor.Decision{NextState: tutor.StateHint}}); err != nil {
				t.Fatal(err)
			}
			if client.calls != 2 || len(auditor.requests) != 1 || !slices.Equal(waits, []time.Duration{2 * time.Second}) {
				t.Fatalf("status=%d generation calls=%d audits=%d waits=%v", status, client.calls, len(auditor.requests), waits)
			}
			t.Logf("status=%d generation calls=%d audits=%d waits=%v", status, client.calls, len(auditor.requests), waits)
		})
	}
}

func TestCodexProviderGeneration429RetryExhaustionIsChildSafe(t *testing.T) {
	client := &structuredClientStub{errors: []error{
		generationResponseError(http.StatusTooManyRequests, 0, false),
		generationResponseError(http.StatusTooManyRequests, 0, false),
		generationResponseError(http.StatusTooManyRequests, 0, false),
	}}
	auditor := passingTutorOutputAuditor()
	provider, err := NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	configureImmediateGenerationRetries(provider, &waits)

	_, err = provider.GenerateTurn(context.Background(), GenerateTurnRequest{TutorDecision: tutor.Decision{NextState: tutor.StateHint}})
	if !errors.Is(err, ErrTutorGenerationBusy) || !IsRetryableResponsesError(err) {
		t.Fatalf("generation exhaustion error=%v", err)
	}
	details, ok := TutorGenerationBusyFailureDetails(err)
	if !ok || details.Category != TutorReviewFailureRetryExhausted || details.HTTPStatus != http.StatusTooManyRequests ||
		details.ProviderCode != "gateway_concurrency_limit" || details.RequestID != client.requests[2].RequestID {
		t.Fatalf("generation exhaustion details=%+v ok=%v", details, ok)
	}
	if err.Error() != ErrTutorGenerationBusy.Error() {
		t.Fatalf("generation error exposed provider details: %q", err)
	}
	if client.calls != 3 || len(auditor.requests) != 0 || !slices.Equal(waits, []time.Duration{2 * time.Second, 4 * time.Second}) {
		t.Fatalf("generation calls=%d audits=%d waits=%v", client.calls, len(auditor.requests), waits)
	}
	t.Logf("generation 429 exhaustion calls=%d audits=%d waits=%v", client.calls, len(auditor.requests), waits)
}

func TestCodexProviderGeneration400DoesNotRetry(t *testing.T) {
	client := &structuredClientStub{errors: []error{generationResponseError(http.StatusBadRequest, 0, false)}}
	auditor := passingTutorOutputAuditor()
	provider, err := NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	configureImmediateGenerationRetries(provider, &waits)

	_, err = provider.GenerateTurn(context.Background(), GenerateTurnRequest{TutorDecision: tutor.Decision{NextState: tutor.StateHint}})
	if err == nil || errors.Is(err, ErrTutorGenerationBusy) {
		t.Fatalf("400 generation error=%v", err)
	}
	if client.calls != 1 || len(auditor.requests) != 0 || len(waits) != 0 {
		t.Fatalf("400 generation calls=%d audits=%d waits=%v", client.calls, len(auditor.requests), waits)
	}
	t.Logf("generation 400 calls=%d audits=%d waits=%v", client.calls, len(auditor.requests), waits)
}

func TestCodexProviderGenerationRetryHonorsRetryAfter(t *testing.T) {
	client := &structuredClientStub{
		errors:  []error{generationResponseError(http.StatusTooManyRequests, 7*time.Second, true), nil},
		results: []StructuredResult{{}, validGeneratedTurn(tutor.StateHint)},
	}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	configureImmediateGenerationRetries(provider, &waits)

	if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{TutorDecision: tutor.Decision{NextState: tutor.StateHint}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(waits, []time.Duration{7 * time.Second}) {
		t.Fatalf("Retry-After waits=%v", waits)
	}
}

func TestCodexProviderGenerationOverallTimeoutStopsRetryWait(t *testing.T) {
	client := &structuredClientStub{errors: []error{generationResponseError(http.StatusTooManyRequests, 0, false)}}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatal(err)
	}
	provider.generationRetry.overallTimeout = 20 * time.Millisecond
	provider.generationRetry.baseDelay = time.Second
	provider.generationRetry.maxJitter = 0
	provider.generationRetryJitter = func(time.Duration) time.Duration { return 0 }

	started := time.Now()
	_, err = provider.GenerateTurn(context.Background(), GenerateTurnRequest{TutorDecision: tutor.Decision{NextState: tutor.StateHint}})
	elapsed := time.Since(started)
	details, ok := TutorGenerationBusyFailureDetails(err)
	if !errors.Is(err, ErrTutorGenerationBusy) || !ok || details.Category != TutorReviewFailureTimeout {
		t.Fatalf("timeout error=%v details=%+v ok=%v", err, details, ok)
	}
	if elapsed > 250*time.Millisecond || client.calls != 1 {
		t.Fatalf("timeout elapsed=%v calls=%d", elapsed, client.calls)
	}
}
