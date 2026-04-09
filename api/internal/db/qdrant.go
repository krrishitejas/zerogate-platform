package db

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

var qdrantHost = "http://localhost:6333"

type QdrantPoint struct {
	ID      string         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload"`
}

func ConnectQdrant(host string) {
	if host != "" {
		qdrantHost = host
	}
}

func InitQdrant() error {
	url := fmt.Sprintf("%s/collections/code_embeddings", qdrantHost)

	// Check if exists
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to reach qdrant check: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		log.Println("Qdrant: collection 'code_embeddings' already exists.")
		return nil
	}

	// Create collection
	payload := map[string]any{
		"vectors": map[string]any{
			"size":     768,
			"distance": "Cosine",
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		resBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("failed to create collection (status %d): %s", res.StatusCode, string(resBody))
	}

	log.Println("Qdrant: collection 'code_embeddings' created successfully.")
	return nil
}

func UpsertEmbeddings(points []QdrantPoint) error {
	if len(points) == 0 {
		return nil
	}

	url := fmt.Sprintf("%s/collections/code_embeddings/points", qdrantHost)
	payload := map[string]any{
		"points": points,
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upsert points: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		resBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("qdrant upsert error (status %d): %s", res.StatusCode, string(resBody))
	}

	return nil
}

// DeleteProjectEmbeddings removes all embeddings for a given project.
func DeleteProjectEmbeddings(projectID string) error {
	url := fmt.Sprintf("%s/collections/code_embeddings/points/delete", qdrantHost)

	payload := map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{
					"key": "project_id",
					"match": map[string]any{
						"value": projectID,
					},
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		log.Printf("Warning: failed to delete project embeddings: %v", err)
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		resBody, _ := io.ReadAll(res.Body)
		log.Printf("Warning: qdrant delete error (status %d): %s", res.StatusCode, string(resBody))
	}

	return nil
}

type SearchResult struct {
	ID      string         `json:"id"`
	Version int            `json:"version"`
	Score   float32        `json:"score"`
	Payload map[string]any `json:"payload"`
}

func SearchEmbeddings(queryVector []float32, projectID string, limit int) ([]SearchResult, error) {
	url := fmt.Sprintf("%s/collections/code_embeddings/points/search", qdrantHost)

	filter := map[string]any{
		"must": []map[string]any{
			{
				"key": "project_id",
				"match": map[string]any{
					"value": projectID,
				},
			},
		},
	}

	payload := map[string]any{
		"vector":       queryVector,
		"limit":        limit,
		"with_payload": true,
		"filter":       filter,
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to search points: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		resBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("qdrant search error (status %d): %s", res.StatusCode, string(resBody))
	}

	var response struct {
		Result []SearchResult `json:"result"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return response.Result, nil
}

// SearchEmbeddingsWithFilters performs a filtered vector search with additional payload filters.
func SearchEmbeddingsWithFilters(queryVector []float32, filters map[string]string, limit int) ([]SearchResult, error) {
	url := fmt.Sprintf("%s/collections/code_embeddings/points/search", qdrantHost)

	mustClauses := []map[string]any{}
	for key, value := range filters {
		mustClauses = append(mustClauses, map[string]any{
			"key": key,
			"match": map[string]any{
				"value": value,
			},
		})
	}

	payload := map[string]any{
		"vector":       queryVector,
		"limit":        limit,
		"with_payload": true,
		"filter": map[string]any{
			"must": mustClauses,
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to search points: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		resBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("qdrant search error (status %d): %s", res.StatusCode, string(resBody))
	}

	var response struct {
		Result []SearchResult `json:"result"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return response.Result, nil
}
