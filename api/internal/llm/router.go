package llm

import (
	"os"
)

// ModelRoute maps a task category to a recommended AI model.
type ModelRoute struct {
	TaskCategory string `json:"task_category"`
	ModelName    string `json:"model_name"`
	Provider     string `json:"provider"` // ollama, vllm, openai, anthropic
	Fallback     string `json:"fallback"`
}

// DefaultRoutes returns the default model routing table per blueprint §8.
func DefaultRoutes() []ModelRoute {
	return []ModelRoute{
		{
			TaskCategory: "security_analysis",
			ModelName:    getEnv("SECURITY_MODEL", "qwen/qwen-2.5-coder-32b-instruct"),
			Provider:     "huggingface",
			Fallback:     "gemini-1.5-flash",
		},
		{
			TaskCategory: "bug_detection",
			ModelName:    getEnv("BUG_MODEL", "bigcode/starcoder2-7b"),
			Provider:     "huggingface",
			Fallback:     "gemini-1.5-flash",
		},
		{
			TaskCategory: "architecture",
			ModelName:    getEnv("ARCHITECTURE_MODEL", "deepseek-ai/DeepSeek-Coder-V2-Base"),
			Provider:     "openrouter", // Usually free on openrouter
			Fallback:     "gemini-1.5-flash",
		},
		{
			TaskCategory: "performance",
			ModelName:    getEnv("PERFORMANCE_MODEL", "meta-llama/CodeLlama-34b-Instruct-hf"),
			Provider:     "openrouter",
			Fallback:     "gemini-1.5-flash",
		},
		{
			TaskCategory: "fix_generation",
			ModelName:    getEnv("AUTOFIX_MODEL", "bigcode/starcoder2-15b-instruct"),
			Provider:     "huggingface",
			Fallback:     "gemini-1.5-flash",
		},
		{
			TaskCategory: "embeddings",
			ModelName:    getEnv("EMBEDDING_MODEL", "BAAI/bge-m3"),
			Provider:     "huggingface",
			Fallback:     "nomic-embed-text",
		},
		{
			TaskCategory: "complex_reasoning",
			ModelName:    getEnv("COMPLEX_MODEL", "gemini-1.5-flash"),
			Provider:     "gemini",
			Fallback:     "gemini-1.5-pro",
		},
	}
}

// GetModelForTask returns the recommended model name for a task category.
func GetModelForTask(taskCategory string) string {
	for _, route := range DefaultRoutes() {
		if route.TaskCategory == taskCategory {
			return route.ModelName
		}
	}
	return "codellama:7b" // default fallback
}

// GetProviderForTask returns the inference provider for a task.
func GetProviderForTask(taskCategory string) string {
	for _, route := range DefaultRoutes() {
		if route.TaskCategory == taskCategory {
			return route.Provider
		}
	}
	return "ollama"
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
