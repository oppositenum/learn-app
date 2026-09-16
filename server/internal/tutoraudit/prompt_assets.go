package tutoraudit

import (
	"embed"
	"fmt"
	"strings"
)

const ReviewPromptVersion = "tutor-output-review-sha256:ea8ee8821dba52c27fd3e1f7862beed5a1d1bc62fa1294a1595f18f094d0b6fd"

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
