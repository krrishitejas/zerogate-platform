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
	modelName := GetModelForTask("fix_generation")
	provider := GetProviderForTask("fix_generation")

	// Try to read the actual source file for context
	codeContext := codeSnippet
	if filePath != "" {
		content, err := readFileContext(filePath, startLine, 30) // 30 lines of context
		if err == nil && content != "" {
			codeContext = content
		}
	}

	var patch string
	var err error
	if provider == "ollama" {
		config := DefaultConfig()
		config.Model = modelName
		patch, err = GenerateCodeFix(config, filePath, codeContext, findingDescription)
	} else {
		systemPrompt := `You are a senior code security and quality expert. Given a code finding and its surrounding code, generate a minimal, targeted fix as a unified diff patch. 
Output ONLY the unified diff patch in standard format, nothing else.
The patch should:
1. Fix the specific issue described
2. Be minimal — change only what's necessary
3. Maintain the existing code style
4. Include proper --- a/ and +++ b/ headers`

		prompt := fmt.Sprintf(`File: %s

Finding: %s

Code Context:
%s

Generate the unified diff patch to fix this issue:`, filePath, findingDescription, codeContext)
		
		response, cErr := GenerateFromCloud(provider, modelName, systemPrompt+"\n\n"+prompt)
		if cErr != nil {
			err = cErr
		} else {
			patch = extractDiffFromResponse(response)
			if patch == "" {
				patch = generateStubPatch(filePath, findingDescription)
			}
		}
	}

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
