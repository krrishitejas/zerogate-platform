package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// GenerateFromCloud attempts to generate a completion using free-tier / cloud APIs based on provider string.
func GenerateFromCloud(provider, model, prompt string) (string, error) {
	switch provider {
	case "huggingface":
		return callHuggingFace(model, prompt)
	case "openrouter":
		return callOpenRouter(model, prompt)
	case "gemini":
		return callGemini(model, prompt)
	case "groq":
		return callGroq(model, prompt)
	default:
		return "", fmt.Errorf("unsupported cloud provider: %s", provider)
	}
}

func callHuggingFace(model, prompt string) (string, error) {
	token := os.Getenv("HF_TOKEN")
	if token == "" {
		fmt.Println("[WARN] HF_TOKEN is empty. Using mock output for HuggingFace.")
		return mockModelResponse(model, prompt), nil
	}

	url := fmt.Sprintf("https://api-inference.huggingface.co/models/%s", model)
	
	payload := map[string]interface{}{
		"inputs": prompt,
		"parameters": map[string]interface{}{
			"max_new_tokens": 1024,
			"temperature":    0.2,
			"return_full_text": false,
		},
	}
	
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HuggingFace API returned %d", resp.StatusCode)
	}

	var res []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if len(res) > 0 {
		if genText, ok := res[0]["generated_text"].(string); ok {
			return genText, nil
		}
	}
	return "", fmt.Errorf("failed to parse huggingface output")
}

func callOpenRouter(model, prompt string) (string, error) {
	token := os.Getenv("OPENROUTER_API_KEY")
	if token == "" {
		fmt.Println("[WARN] OPENROUTER_API_KEY is empty. Using mock output for OpenRouter.")
		return mockModelResponse(model, prompt), nil
	}

	url := "https://openrouter.ai/api/v1/chat/completions"
	payload := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	return parseOpenAICompatibleResponse(req)
}

func callGroq(model, prompt string) (string, error) {
	token := os.Getenv("GROQ_API_KEY")
	if token == "" {
		fmt.Println("[WARN] GROQ_API_KEY is empty. Using mock output for Groq.")
		return mockModelResponse(model, prompt), nil
	}

	url := "https://api.groq.com/openai/v1/chat/completions"
	payload := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	return parseOpenAICompatibleResponse(req)
}

func callGemini(model, prompt string) (string, error) {
	token := os.Getenv("GEMINI_API_KEY")
	if token == "" {
		fmt.Println("[WARN] GEMINI_API_KEY is empty. Using mock output for Gemini.")
		return mockModelResponse(model, prompt), nil
	}
	
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, token)
	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]string{
					{"text": prompt},
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Gemini returned %d: %s", resp.StatusCode, string(b))
	}
	
	var res map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	
	// Fast parsing of Google's nested response struct
	if cands, ok := res["candidates"].([]interface{}); ok && len(cands) > 0 {
		if cand, ok := cands[0].(map[string]interface{}); ok {
			if content, ok := cand["content"].(map[string]interface{}); ok {
				if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
					if part, ok := parts[0].(map[string]interface{}); ok {
						if text, ok := part["text"].(string); ok {
							return text, nil
						}
					}
				}
			}
		}
	}
	return "", fmt.Errorf("failed to parse gemini response")
}

func parseOpenAICompatibleResponse(req *http.Request) (string, error) {
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Provider returned %d: %s", resp.StatusCode, string(b))
	}

	var res map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if choices, ok := res["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					return content, nil
				}
			}
		}
	}

	return "", fmt.Errorf("failed to parse openai-compatible output")
}

// mockModelResponse handles cases where the user does not have API keys hooked up.
// It acts as a fallback so testing the platform structure doesn't crash.
func mockModelResponse(model, prompt string) string {
	return fmt.Sprintf("MOCK [%s] API RESPONSE: Cloud Integration Active. Provide actual keys to see true LLM output. Received %d char prompt.", model, len(prompt))
}

// GenerateEmbeddingFromCloud fetches embeddings using HuggingFace feature-extraction API.
func GenerateEmbeddingFromCloud(provider, model, text string) ([]float32, error) {
	if provider != "huggingface" {
		return nil, fmt.Errorf("only huggingface provider is currently implemented for cloud embeddings")
	}

	token := os.Getenv("HF_TOKEN")
	if token == "" {
		fmt.Println("[WARN] HF_TOKEN is empty. Failing embedding to trigger fallback mock.")
		return nil, fmt.Errorf("missing HF_TOKEN")
	}

	url := fmt.Sprintf("https://api-inference.huggingface.co/pipeline/feature-extraction/%s", model)
	payload := map[string]interface{}{
		"inputs": []string{text},
	}
	
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HuggingFace feature-extraction returned %d", resp.StatusCode)
	}

	var res []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	vector := make([]float32, 0)
	// BGE-M3 returns a nested array for batches
	if len(res) > 0 {
		if inner, ok := res[0].([]interface{}); ok {
			for _, val := range inner {
				if v, ok := val.(float64); ok {
					vector = append(vector, float32(v))
				}
			}
		} else {
			// Direct array
			for _, val := range res {
				if v, ok := val.(float64); ok {
					vector = append(vector, float32(v))
				}
			}
		}
	}

	if len(vector) > 0 {
		return vector, nil
	}
	
	return nil, fmt.Errorf("failed to parse embedding output securely")
}
