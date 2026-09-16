package ai

import (
	"embed"
	"fmt"
	"strings"
)

const GenerationPromptVersion = "tutor-generation-sha256:ebd7afe086e10cd8ce3e9b3ef2d93586ade7da4e5e2b0950cb6f15253ad157de"

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
)
