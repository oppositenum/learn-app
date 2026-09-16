package tutoraudit

import (
	"embed"
	"fmt"
	"strings"
)

// ReviewPromptVersion covers every embedded review asset: the instruction
// file, the failure contract, and the structured-output examples.
const ReviewPromptVersion = "tutor-output-review-sha256:52d2be162c0aa1f864f9244c01a56d3c1e0f9a0f488f19cf3158ad5a1a6e6d3a"

//go:embed prompts/review/*.instructions.txt prompts/review/*.md prompts/review/examples/*.json
var reviewPromptFiles embed.FS

func reviewPrompt(filename string) string {
	contents, err := reviewPromptFiles.ReadFile("prompts/review/" + filename)
	if err != nil {
		panic(fmt.Sprintf("read embedded review prompt %s: %v", filename, err))
	}
	return strings.TrimSuffix(string(contents), "\n")
}

var reviewerInstructions = reviewPrompt("reviewer.instructions.txt")
