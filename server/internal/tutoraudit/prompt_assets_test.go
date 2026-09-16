package tutoraudit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	aioutputs "github.com/oppositenum/ai-learning-tutor/schemas/ai_outputs"
)

const reviewerPromptBaseline = "Act only as an independent answer-disclosure reviewer for a child-facing Tutor response. Compare the candidate message and every segment with the original question and private answer. REJECT only when the candidate directly states the correct answer, uniquely determines the correct answer through equivalent wording or conversion, or provides a complete solution that replaces the student's reasoning. PASS when the candidate only points to an observation direction, identifies a type of evidence to consider, gives a method framework, or asks a follow-up question. For PASS, return no_answer_leak=true, reason_codes=[NONE], and an empty violations array. For REJECT, return no_answer_leak=false, one or more disclosure reason_codes, and violations whose violation_type set exactly matches reason_codes. Use payload_kind=MESSAGE with segment_index=-1 for the message, or payload_kind=SEGMENT with its zero-based segment_index for a segment; never duplicate a violation. Return only the structured violation enums and indices required by the schema; never output a free-text reason. Do not rewrite the candidate and do not perform unrelated safety classification."

func TestReviewPromptMigrationPreservesExactText(t *testing.T) {
	if reviewerInstructions != reviewerPromptBaseline {
		t.Fatalf("review prompt changed during asset migration\ngot:  %q\nwant: %q", reviewerInstructions, reviewerPromptBaseline)
	}
}

func TestReviewPromptVersionMatchesContent(t *testing.T) {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("reviewer.instructions.txt"))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(reviewerInstructions))
	_, _ = hasher.Write([]byte{0})
	want := "tutor-output-review-sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if ReviewPromptVersion != want {
		t.Fatalf("review prompt content requires a version update: got %q want %q", ReviewPromptVersion, want)
	}
}

func TestReviewExamplesSatisfySchemaAndVerdictConsistency(t *testing.T) {
	rawSchema, err := fs.ReadFile(aioutputs.Files, reviewSchemaFile)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(rawSchema, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(reviewSchemaFile, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(reviewSchemaFile)
	if err != nil {
		t.Fatal(err)
	}

	examples, err := fs.ReadDir(reviewPromptFiles, "prompts/review/examples")
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) < 3 {
		t.Fatalf("review examples=%d want at least 3", len(examples))
	}
	for _, example := range examples {
		raw, err := fs.ReadFile(reviewPromptFiles, "prompts/review/examples/"+example.Name())
		if err != nil {
			t.Fatal(err)
		}
		var untyped any
		if err := json.Unmarshal(raw, &untyped); err != nil {
			t.Fatalf("decode %s: %v", example.Name(), err)
		}
		if err := schema.Validate(untyped); err != nil {
			t.Errorf("%s does not satisfy schema: %v", example.Name(), err)
		}
		var review Review
		if err := json.Unmarshal(raw, &review); err != nil {
			t.Fatalf("decode typed %s: %v", example.Name(), err)
		}
		if err := validateReviewVerdict(review, 1); err != nil {
			t.Errorf("%s has inconsistent verdict: %v", example.Name(), err)
		}
	}
}

func TestReviewFailureContractNamesFailClosedCases(t *testing.T) {
	contract := reviewPrompt("failure-contract.md")
	for _, required := range []string{"Timeout", "HTTP 429 or 5xx", "Invalid local JSON Schema", "source mismatch", "inconsistent violations", "persistence failure"} {
		if !strings.Contains(contract, required) {
			t.Errorf("review failure contract does not name %q", required)
		}
	}
}
