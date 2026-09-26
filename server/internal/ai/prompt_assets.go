package ai

import (
	"embed"
	"fmt"
	"strings"
)

// GenerationPromptVersion covers every embedded generation asset: the
// instruction files, the failure contract, and the structured-output examples.
const GenerationPromptVersion = "tutor-generation-sha256:d26ad08e00ee087b2fc7b315342bc2f258de59d67e9e460a4976e6178a1e09a2"

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
	teachingContextInstructions         = generationPrompt("teaching_context.instructions.txt")
	languageReadingInstructions         = generationPrompt("subject_language_reading.instructions.txt")
	mathGoalInstructions                = generationPrompt("subject_math_goal.instructions.txt")
	chineseInstructions                 = generationPrompt("subject_chinese.instructions.txt")
	physicsInstructions                 = generationPrompt("subject_physics.instructions.txt")
	chemistryInstructions               = generationPrompt("subject_chemistry.instructions.txt")
)
