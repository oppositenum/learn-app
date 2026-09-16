package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	aioutputs "github.com/oppositenum/ai-learning-tutor/schemas/ai_outputs"
)

var generationPromptBaseline = map[string]string{
	"analyze_answer.instructions.txt":            "Analyze the answer. Return diagnosis only; never mutate mastery or planning state.",
	"generate_turn.instructions.txt":             "Generate exactly the Tutor action authorized by the server decision. Do not reveal the original answer.",
	"generate_analogy.instructions.txt":          "Generate a short life analogy without revealing the original answer.",
	"generate_parallel_example.instructions.txt": "Explain with a parallel example using different values. Do not solve the original question.",
	"generate_explanation.instructions.txt":      "Give a concise explanation or voice script using a parallel example. Do not reveal the original answer unless the server explicitly authorizes it.",
	"turn_style.instructions.txt":                "Write the message in warm, conversational Chinese for a primary or junior-secondary student. Keep it brief. Plain text only: never use markdown syntax such as headings, asterisks, bullet or dash list markers. Never directly quote, repeat, or equivalently paraphrase a sentence or phrase that uniquely supports the correct answer. Do not locate evidence for the student. Never provide the answer or a complete solution.",
	"hint.instructions.txt":                      "For a HINT, provide only an observation direction, a reasoning method, or a next-step question.",
}

func TestGenerationPromptMigrationPreservesExactText(t *testing.T) {
	for filename, baseline := range generationPromptBaseline {
		if got := generationPrompt(filename); got != baseline {
			t.Errorf("%s changed during asset migration\ngot:  %q\nwant: %q", filename, got, baseline)
		}
	}
}

func TestGenerationPromptVersionMatchesContent(t *testing.T) {
	hash := generationPromptHash(t)
	want := "tutor-generation-sha256:" + hash
	if GenerationPromptVersion != want {
		t.Fatalf("generation prompt content requires a version update: got %q want %q", GenerationPromptVersion, want)
	}
}

// generationPromptHash covers every embedded generation asset, not only the
// instruction files: the failure contract and the structured-output examples
// are part of the prompt asset and must not change without a version bump.
func generationPromptHash(t *testing.T) string {
	t.Helper()
	paths := embeddedPromptPaths(t, generationPromptFiles, "prompts/generation")
	if len(paths) < len(generationPromptBaseline)+2 {
		t.Fatalf("generation prompt hash covers only %d file(s): %v", len(paths), paths)
	}
	hasher := sha256.New()
	for _, path := range paths {
		contents, err := fs.ReadFile(generationPromptFiles, path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = hasher.Write([]byte(path))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(contents)
		_, _ = hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func embeddedPromptPaths(t *testing.T, files fs.FS, root string) []string {
	t.Helper()
	var paths []string
	if err := fs.WalkDir(files, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return paths
}

func TestGenerationExamplesSatisfyLocalSchemas(t *testing.T) {
	compiler := jsonschema.NewCompiler()
	compiled := make(map[string]*jsonschema.Schema, 2)
	for _, schemaName := range []string{"analyze_answer.schema.json", "tutor_turn.schema.json"} {
		raw, err := fs.ReadFile(aioutputs.Files, schemaName)
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		if err := compiler.AddResource(schemaName, document); err != nil {
			t.Fatal(err)
		}
		compiled[schemaName], err = compiler.Compile(schemaName)
		if err != nil {
			t.Fatal(err)
		}
	}

	examples, err := fs.ReadDir(generationPromptFiles, "prompts/generation/examples")
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) < 3 {
		t.Fatalf("generation examples=%d want at least 3", len(examples))
	}
	seenSchemas := map[string]bool{}
	for _, example := range examples {
		raw, err := fs.ReadFile(generationPromptFiles, "prompts/generation/examples/"+example.Name())
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatalf("decode %s: %v", example.Name(), err)
		}
		schemaName := "tutor_turn.schema.json"
		if strings.HasPrefix(example.Name(), "analyze_answer.") {
			schemaName = "analyze_answer.schema.json"
		}
		if err := compiled[schemaName].Validate(value); err != nil {
			t.Errorf("%s does not satisfy %s: %v", example.Name(), schemaName, err)
		}
		seenSchemas[schemaName] = true
	}
	for _, schemaName := range []string{"analyze_answer.schema.json", "tutor_turn.schema.json"} {
		if !seenSchemas[schemaName] {
			t.Errorf("no example covers %s", schemaName)
		}
	}
}

func TestGenerationFailureContractNamesFailClosedCases(t *testing.T) {
	contract := generationPrompt("failure-contract.md")
	for _, required := range []string{"Timeout", "HTTP 429 or 5xx", "Invalid local JSON Schema", "Accounting failure", "source mismatch", "inconsistent violation"} {
		if !strings.Contains(contract, required) {
			t.Errorf("generation failure contract does not name %q", required)
		}
	}
}
