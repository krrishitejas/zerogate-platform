package embedding

import (
	"crypto/sha256"
	"encoding/binary"
	"log"
	"math/rand"

	"github.com/zerogate/api/internal/llm"
)

// GenerateEmbedding creates a 768-dimensional embedding for the given text.
// Uses Ollama's embedding API if available, falls back to deterministic mock.
func GenerateEmbedding(text string) ([]float32, error) {
	// Try real embedding via Ollama first
	if llm.IsOllamaAvailable() {
		vector, err := llm.GenerateEmbeddingOllama("nomic-embed-text", text)
		if err != nil {
			log.Printf("Ollama embedding failed, using mock: %v", err)
			return generateMockEmbedding(text), nil
		}

		// If the model returns a different dimension, pad/truncate to 768
		if len(vector) == 768 {
			return vector, nil
		}
		if len(vector) > 768 {
			return vector[:768], nil
		}
		// Pad with zeros
		padded := make([]float32, 768)
		copy(padded, vector)
		return padded, nil
	}

	// Fallback: deterministic mock embedding based on text hash
	return generateMockEmbedding(text), nil
}

// generateMockEmbedding creates a deterministic 768-dim vector based on text hash.
func generateMockEmbedding(text string) []float32 {
	h := sha256.New()
	h.Write([]byte(text))
	hashSum := h.Sum(nil)

	seed := int64(binary.BigEndian.Uint64(hashSum[:8]))
	rng := rand.New(rand.NewSource(seed))

	vector := make([]float32, 768)
	for i := 0; i < 768; i++ {
		vector[i] = float32(rng.NormFloat64() * 0.1)
	}

	return vector
}
