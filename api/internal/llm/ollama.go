package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// OllamaConfig holds the configuration for the Ollama API client.
type OllamaConfig struct {
	Endpoint    string
	Model       string
	Temperature float64
	MaxTokens   int
	Timeout     time.Duration
}

// DefaultConfig returns the default Ollama configuration.
func DefaultConfig() OllamaConfig {
	endpoint := os.Getenv("OLLAMA_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = "codellama:7b"
	}
	return OllamaConfig{
		Endpoint:    endpoint,
		Model:       model,
		Temperature: 0.2,
		MaxTokens:   4096,
		Timeout:     120 * time.Second,
	}
}

// ollamaGenerateRequest is the request body for /api/generate
type ollamaGenerateRequest struct {
	Model   string            `json:"model"`
	Prompt  string            `json:"prompt"`
	System  string            `json:"system,omitempty"`
	Stream  bool              `json:"stream"`
	Options map[string]any    `json:"options,omitempty"`
}

// ollamaGenerateResponse is the response body from /api/generate
type ollamaGenerateResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// ollamaEmbedRequest is the request body for /api/embeddings
type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// ollamaEmbedResponse is the response body from /api/embeddings
type ollamaEmbedResponse struct {
	Embedding []float64 `json:"embedding"`
}

// IsOllamaAvailable checks if Ollama is reachable.
func IsOllamaAvailable() bool {
	cfg := DefaultConfig()
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(cfg.Endpoint + "/api/tags")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// AnalyzeCode sends code to an Ollama model for analysis and returns the response text.
func AnalyzeCode(config OllamaConfig, systemPrompt, codeContext string) (string, error) {
	reqBody := ollamaGenerateRequest{
		Model:  config.Model,
		Prompt: codeContext,
		System: systemPrompt,
		Stream: false,
		Options: map[string]any{
			"temperature":  config.Temperature,
			"num_predict":  config.MaxTokens,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	client := &http.Client{Timeout: config.Timeout}
	resp, err := client.Post(config.Endpoint+"/api/generate", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Response, nil
}

// GenerateEmbeddingOllama generates a real embedding vector using Ollama.
func GenerateEmbeddingOllama(model, text string) ([]float32, error) {
	cfg := DefaultConfig()
	if model == "" {
		model = "nomic-embed-text"
	}

	reqBody := ollamaEmbedRequest{
		Model:  model,
		Prompt: text,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(cfg.Endpoint+"/api/embeddings", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embedding returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}

	// Convert float64 to float32
	vector := make([]float32, len(result.Embedding))
	for i, v := range result.Embedding {
		vector[i] = float32(v)
	}

	return vector, nil
}

// ParseFindingsFromLLM attempts to parse structured findings from LLM response text.
// The LLM is prompted to output JSON, but responses may be messy.
type LLMFinding struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Severity    string  `json:"severity"`
	Category    string  `json:"category"`
	LineStart   int     `json:"line_start"`
	LineEnd     int     `json:"line_end"`
	CweID       string  `json:"cwe_id,omitempty"`
	CvssScore   float64 `json:"cvss_score,omitempty"`
	RootCause   string  `json:"root_cause,omitempty"`
	Impact      string  `json:"impact,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`
}

// ParseFindingsJSON extracts findings from LLM response that contains JSON.
func ParseFindingsJSON(response string) ([]LLMFinding, error) {
	// Try to find JSON array in the response
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start == -1 || end == -1 || end <= start {
		// Try single object
		start = strings.Index(response, "{")
		end = strings.LastIndex(response, "}")
		if start == -1 || end == -1 || end <= start {
			return nil, fmt.Errorf("no JSON found in LLM response")
		}
		jsonStr := response[start : end+1]
		var finding LLMFinding
		if err := json.Unmarshal([]byte(jsonStr), &finding); err != nil {
			return nil, fmt.Errorf("failed to parse single finding JSON: %w", err)
		}
		return []LLMFinding{finding}, nil
	}

	jsonStr := response[start : end+1]
	var findings []LLMFinding
	if err := json.Unmarshal([]byte(jsonStr), &findings); err != nil {
		return nil, fmt.Errorf("failed to parse findings JSON array: %w", err)
	}

	return findings, nil
}

// GenerateCodeFix generates a unified diff patch for a finding using an LLM.
func GenerateCodeFix(config OllamaConfig, filePath, codeContext, findingDescription string) (string, error) {
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

	response, err := AnalyzeCode(config, systemPrompt, prompt)
	if err != nil {
		// Fallback to stub patch if Ollama is unavailable
		log.Printf("LLM unavailable for fix generation, using stub patch: %v", err)
		return generateStubPatch(filePath, findingDescription), nil
	}

	// Extract diff from response
	patch := extractDiffFromResponse(response)
	if patch == "" {
		return generateStubPatch(filePath, findingDescription), nil
	}

	return patch, nil
}

// generateStubPatch creates a minimal placeholder patch when LLM is unavailable.
func generateStubPatch(filePath, description string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", filePath, filePath))
	sb.WriteString("@@ -1,1 +1,2 @@\n")
	sb.WriteString(fmt.Sprintf("+ // TODO: ZEROGATE AUTO-FIX REQUIRED: %s\n", description))
	return sb.String()
}

// extractDiffFromResponse tries to extract a unified diff from an LLM response.
func extractDiffFromResponse(response string) string {
	lines := strings.Split(response, "\n")
	var diffLines []string
	inDiff := false

	for _, line := range lines {
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "@@") {
			inDiff = true
		}
		if inDiff {
			diffLines = append(diffLines, line)
		}
		// Also capture lines starting with + - or space after @@ header
		if inDiff && !strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "+++") &&
			!strings.HasPrefix(line, "@@") && !strings.HasPrefix(line, "+") &&
			!strings.HasPrefix(line, "-") && !strings.HasPrefix(line, " ") && line != "" {
			// End of diff block
			break
		}
	}

	if len(diffLines) > 0 {
		return strings.Join(diffLines, "\n")
	}
	return ""
}
