package llm

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// GeneratePatch generates a fix patch for a finding using an LLM.
// Reads actual source code context from the file and sends it to the model.
func GeneratePatch(filePath string, codeSnippet string, findingDescription string, startLine int) (string, error) {
	config := DefaultConfig()
	config.Model = os.Getenv("AUTOFIX_MODEL")
	if config.Model == "" {
		config.Model = "codellama:7b"
	}

	// Try to read the actual source file for context
	codeContext := codeSnippet
	if filePath != "" {
		content, err := readFileContext(filePath, startLine, 30) // 30 lines of context
		if err == nil && content != "" {
			codeContext = content
		}
	}

	patch, err := GenerateCodeFix(config, filePath, codeContext, findingDescription)
	if err != nil {
		log.Printf("LLM patch generation failed, using stub: %v", err)
		return generateStubPatch(filePath, findingDescription), nil
	}

	return patch, nil
}

// readFileContext reads lines around a target line from a file.
func readFileContext(filePath string, centerLine, contextLines int) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(data), "\n")
	startIdx := centerLine - contextLines/2 - 1
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := startIdx + contextLines
	if endIdx > len(lines) {
		endIdx = len(lines)
	}

	var sb strings.Builder
	for i := startIdx; i < endIdx; i++ {
		sb.WriteString(fmt.Sprintf("%d: %s\n", i+1, lines[i]))
	}

	return sb.String(), nil
}
