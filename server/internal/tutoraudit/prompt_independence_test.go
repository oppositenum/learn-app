package tutoraudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerationAndReviewPromptAssetsAreIndependent(t *testing.T) {
	generationPaths, err := filepath.Glob(filepath.Join("..", "ai", "prompts", "generation", "*.instructions.txt"))
	if err != nil {
		t.Fatal(err)
	}
	reviewPaths, err := filepath.Glob(filepath.Join("prompts", "review", "*.instructions.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(generationPaths) == 0 || len(reviewPaths) == 0 {
		t.Fatalf("prompt paths generation=%v review=%v", generationPaths, reviewPaths)
	}
	for _, generationPath := range generationPaths {
		generationText := readPromptFile(t, generationPath)
		for _, reviewPath := range reviewPaths {
			if generationPath == reviewPath {
				t.Fatalf("generation and review prompt use the same file %s", generationPath)
			}
			reviewText := readPromptFile(t, reviewPath)
			if generationText == reviewText || strings.Contains(generationText, reviewText) || strings.Contains(reviewText, generationText) {
				t.Fatalf("prompt assets are equal or substrings: generation=%s review=%s", generationPath, reviewPath)
			}
		}
	}
}

func readPromptFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(contents), "\n")
}
