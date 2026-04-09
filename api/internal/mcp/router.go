package mcp

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
			ModelName:    getEnv("SECURITY_MODEL", "qwen2.5-coder:32b"),
			Provider:     "ollama",
			Fallback:     "claude-4",
		},
		{
			TaskCategory: "bug_detection",
			ModelName:    getEnv("BUG_MODEL", "starcoder2:7b"),
			Provider:     "ollama",
			Fallback:     "claude-4",
		},
		{
			TaskCategory: "architecture",
			ModelName:    getEnv("ARCHITECTURE_MODEL", "deepseek-coder-v2:33b"),
			Provider:     "ollama",
			Fallback:     "claude-4",
		},
		{
			TaskCategory: "performance",
			ModelName:    getEnv("PERFORMANCE_MODEL", "codellama:34b"),
			Provider:     "ollama",
			Fallback:     "claude-4",
		},
		{
			TaskCategory: "fix_generation",
			ModelName:    getEnv("AUTOFIX_MODEL", "starcoder2:15b"),
			Provider:     "ollama",
			Fallback:     "claude-4",
		},
		{
			TaskCategory: "embeddings",
			ModelName:    getEnv("EMBEDDING_MODEL", "bge-m3"),
			Provider:     "ollama",
			Fallback:     "nomic-embed-text",
		},
		{
			TaskCategory: "complex_reasoning",
			ModelName:    getEnv("COMPLEX_MODEL", "claude-4"),
			Provider:     "anthropic",
			Fallback:     "gpt-4.1",
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
