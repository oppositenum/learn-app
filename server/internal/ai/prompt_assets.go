package ai

import (
	"embed"
	"fmt"
	"strings"
)

// GenerationPromptVersion covers every embedded generation asset: the
// instruction files, the failure contract, and the structured-output examples.
const GenerationPromptVersion = "tutor-generation-sha256:1cbe59a0b38a04ab3c51686cdea0cd25fb73e3245002e43f95e782e81716ec0f"

//go:embed prompts/generation/*.instructions.txt prompts/generation/*.md prompts/generation/examples/*.json
var generationPromptFiles embed.FS

func generationPrompt(filename string) string {
	contents, err := generationPromptFiles.ReadFile("prompts/generation/" + filename)
	if err != nil {
		panic(fmt.Sprintf("read embedded generation prompt %s: %v", filename, err))
	}
	return strings.TrimSuffix(string(contents), "\n")
}

var (
	analyzeAnswerInstructions           = generationPrompt("analyze_answer.instructions.txt")
	generateTurnInstructions            = generationPrompt("generate_turn.instructions.txt")
	generateAnalogyInstructions         = generationPrompt("generate_analogy.instructions.txt")
	generateParallelExampleInstructions = generationPrompt("generate_parallel_example.instructions.txt")
	generateExplanationInstructions     = generationPrompt("generate_explanation.instructions.txt")
	turnStyleInstructions               = generationPrompt("turn_style.instructions.txt")
	hintInstructions                    = generationPrompt("hint.instructions.txt")
	materialDisciplineInstructions      = generationPrompt("material_discipline.instructions.txt")
	outputShapeInstructions             = generationPrompt("output_shape.instructions.txt")
)
