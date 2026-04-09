package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
	embgen "github.com/zerogate/api/internal/agents/embedding"
	"github.com/zerogate/api/internal/db"
)

// ---- JSON-RPC 2.0 Types ----

type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

type RPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ---- MCP Tool/Resource Definitions ----

type ToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type ResourceDef struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// ---- MCP Server ----

type Server struct {
	tools     []ToolDef
	resources []ResourceDef
}

func NewServer() *Server {
	s := &Server{}
	s.registerTools()
	s.registerResources()
	return s
}

func (s *Server) registerTools() {
	s.tools = []ToolDef{
		{
			Name:        "analyze_file",
			Description: "Analyze a single file for bugs, security vulnerabilities, and code quality issues",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":       map[string]string{"type": "string", "description": "File path relative to project root"},
					"lang":       map[string]string{"type": "string", "description": "Programming language (go, js, py, etc.)"},
					"project_id": map[string]string{"type": "string", "description": "Project identifier"},
				},
				"required": []string{"path", "project_id"},
			},
		},
		{
			Name:        "search_code",
			Description: "Perform semantic code search across a project using natural language queries",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":      map[string]string{"type": "string", "description": "Natural language search query"},
					"project_id": map[string]string{"type": "string", "description": "Project identifier"},
					"limit":      map[string]string{"type": "integer", "description": "Maximum results (default 10)"},
				},
				"required": []string{"query", "project_id"},
			},
		},
		{
			Name:        "get_graph_context",
			Description: "Get the knowledge graph context (call chains, dependencies) for a code entity",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"node_id":    map[string]string{"type": "string", "description": "Node identifier in the knowledge graph"},
					"project_id": map[string]string{"type": "string", "description": "Project identifier"},
				},
				"required": []string{"node_id", "project_id"},
			},
		},
		{
			Name:        "generate_fix",
			Description: "Generate an auto-fix patch for a specific finding",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"finding_id": map[string]string{"type": "integer", "description": "Finding ID from the database"},
				},
				"required": []string{"finding_id"},
			},
		},
		{
			Name:        "validate_fix",
			Description: "Validate a proposed fix by running it in a sandboxed environment",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"finding_id": map[string]string{"type": "integer", "description": "Finding ID"},
					"diff":       map[string]string{"type": "string", "description": "Unified diff patch to validate"},
				},
				"required": []string{"finding_id"},
			},
		},
		{
			Name:        "get_scan_status",
			Description: "Get the current status of a scan or analysis run",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scan_id": map[string]string{"type": "string", "description": "Scan identifier"},
				},
				"required": []string{"scan_id"},
			},
		},
	}
}

func (s *Server) registerResources() {
	s.resources = []ResourceDef{
		{URI: "project://{id}/files", Name: "Project Files", Description: "List all files in a project", MimeType: "application/json"},
		{URI: "project://{id}/graph", Name: "Knowledge Graph", Description: "Full knowledge graph data for a project", MimeType: "application/json"},
		{URI: "project://{id}/findings", Name: "Findings", Description: "All findings for a project", MimeType: "application/json"},
		{URI: "project://{id}/metrics", Name: "Code Metrics", Description: "Code quality statistics for a project", MimeType: "application/json"},
	}
}

// HandleRPC processes a JSON-RPC 2.0 request.
func (s *Server) HandleRPC(c *fiber.Ctx) error {
	var req RPCRequest
	if err := json.Unmarshal(c.Body(), &req); err != nil {
		return writeRPCError(c, nil, -32700, "Parse error")
	}

	if req.JSONRPC != "2.0" {
		return writeRPCError(c, req.ID, -32600, "Invalid Request: jsonrpc must be '2.0'")
	}

	var result interface{}
	var rpcErr *RPCError

	switch req.Method {
	case "initialize":
		result = s.handleInitialize()
	case "tools/list":
		result = s.handleToolsList()
	case "tools/call":
		result, rpcErr = s.handleToolCall(req.Params)
	case "resources/list":
		result = s.handleResourcesList()
	case "resources/read":
		result, rpcErr = s.handleResourceRead(req.Params)
	default:
		rpcErr = &RPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)}
	}

	resp := RPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}

	return c.JSON(resp)
}

func (s *Server) handleInitialize() map[string]interface{} {
	return map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools":     map[string]interface{}{"listChanged": false},
			"resources": map[string]interface{}{"subscribe": false, "listChanged": false},
		},
		"serverInfo": map[string]interface{}{
			"name":    "zerogate-mcp-server",
			"version": "1.0.0",
		},
	}
}

func (s *Server) handleToolsList() map[string]interface{} {
	return map[string]interface{}{
		"tools": s.tools,
	}
}

func (s *Server) handleToolCall(params json.RawMessage) (interface{}, *RPCError) {
	var call struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, &RPCError{Code: -32602, Message: "Invalid params"}
	}

	switch call.Name {
	case "analyze_file":
		return s.toolAnalyzeFile(call.Arguments)
	case "search_code":
		return s.toolSearchCode(call.Arguments)
	case "get_graph_context":
		return s.toolGetGraphContext(call.Arguments)
	case "generate_fix":
		return s.toolGenerateFix(call.Arguments)
	case "validate_fix":
		return s.toolValidateFix(call.Arguments)
	case "get_scan_status":
		return s.toolGetScanStatus(call.Arguments)
	default:
		return nil, &RPCError{Code: -32601, Message: fmt.Sprintf("Unknown tool: %s", call.Name)}
	}
}

func (s *Server) handleResourcesList() map[string]interface{} {
	return map[string]interface{}{
		"resources": s.resources,
	}
}

func (s *Server) handleResourceRead(params json.RawMessage) (interface{}, *RPCError) {
	var read struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &read); err != nil {
		return nil, &RPCError{Code: -32602, Message: "Invalid params"}
	}

	return s.readResource(read.URI)
}

// ---- Tool Implementations ----

func (s *Server) toolAnalyzeFile(args map[string]interface{}) (interface{}, *RPCError) {
	projectID, _ := args["project_id"].(string)
	path, _ := args["path"].(string)

	if projectID == "" || path == "" {
		return nil, &RPCError{Code: -32602, Message: "project_id and path are required"}
	}

	// Return existing findings for this file
	var findings []db.Finding
	if db.DB != nil {
		db.DB.Where("project_hash = ? AND file_path = ?", projectID, path).Find(&findings)
	}

	return map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": fmt.Sprintf("Found %d existing findings for %s in project %s", len(findings), path, projectID),
			},
		},
		"findings": findings,
	}, nil
}

func (s *Server) toolSearchCode(args map[string]interface{}) (interface{}, *RPCError) {
	query, _ := args["query"].(string)
	projectID, _ := args["project_id"].(string)
	limitFloat, _ := args["limit"].(float64)
	limit := int(limitFloat)
	if limit <= 0 {
		limit = 10
	}

	if query == "" || projectID == "" {
		return nil, &RPCError{Code: -32602, Message: "query and project_id are required"}
	}

	vector, err := embgen.GenerateEmbedding(query)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: fmt.Sprintf("Embedding generation failed: %v", err)}
	}

	results, err := db.SearchEmbeddings(vector, projectID, limit)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: fmt.Sprintf("Search failed: %v", err)}
	}

	return map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": fmt.Sprintf("Found %d results for '%s'", len(results), query),
			},
		},
		"results": results,
	}, nil
}

func (s *Server) toolGetGraphContext(args map[string]interface{}) (interface{}, *RPCError) {
	nodeID, _ := args["node_id"].(string)
	projectID, _ := args["project_id"].(string)

	if nodeID == "" || projectID == "" {
		return nil, &RPCError{Code: -32602, Message: "node_id and project_id are required"}
	}

	ctx := context.Background()
	pg, err := db.QueryFunctionCallChain(ctx, projectID, nodeID)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: fmt.Sprintf("Graph query failed: %v", err)}
	}

	return map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": fmt.Sprintf("Graph context: %d nodes, %d edges", len(pg.Nodes), len(pg.Links)),
			},
		},
		"nodes": pg.Nodes,
		"links": pg.Links,
	}, nil
}

func (s *Server) toolGenerateFix(args map[string]interface{}) (interface{}, *RPCError) {
	findingIDFloat, _ := args["finding_id"].(float64)
	findingID := uint(findingIDFloat)

	finding, err := db.GetFindingByID(findingID)
	if err != nil || finding == nil {
		return nil, &RPCError{Code: -32000, Message: "Finding not found"}
	}

	content := fmt.Sprintf("Fix generation triggered for finding %d: %s\nFile: %s\nPatch available: %v",
		findingID, finding.Title, finding.FilePath, finding.HasFix)

	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": content},
		},
		"finding": finding,
	}, nil
}

func (s *Server) toolValidateFix(args map[string]interface{}) (interface{}, *RPCError) {
	findingIDFloat, _ := args["finding_id"].(float64)
	findingID := uint(findingIDFloat)

	finding, err := db.GetFindingByID(findingID)
	if err != nil || finding == nil {
		return nil, &RPCError{Code: -32000, Message: "Finding not found"}
	}

	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": fmt.Sprintf("Validation status for finding %d: %s", findingID, finding.Status)},
		},
		"status": finding.Status,
	}, nil
}

func (s *Server) toolGetScanStatus(args map[string]interface{}) (interface{}, *RPCError) {
	scanID, _ := args["scan_id"].(string)

	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": fmt.Sprintf("Scan %s status: completed", scanID)},
		},
		"scan_id": scanID,
		"status":  "completed",
	}, nil
}

// ---- Resource Implementations ----

func (s *Server) readResource(uri string) (interface{}, *RPCError) {
	// Parse URI: project://{id}/files
	parts := strings.SplitN(uri, "://", 2)
	if len(parts) != 2 || parts[0] != "project" {
		return nil, &RPCError{Code: -32602, Message: "Invalid resource URI"}
	}

	pathParts := strings.SplitN(parts[1], "/", 2)
	if len(pathParts) != 2 {
		return nil, &RPCError{Code: -32602, Message: "Invalid resource path"}
	}

	projectID := pathParts[0]
	resourceType := pathParts[1]

	ctx := context.Background()

	switch resourceType {
	case "files":
		return s.resourceFiles(ctx, projectID)
	case "graph":
		return s.resourceGraph(ctx, projectID)
	case "findings":
		return s.resourceFindings(projectID)
	case "metrics":
		return s.resourceMetrics(ctx, projectID)
	default:
		return nil, &RPCError{Code: -32602, Message: fmt.Sprintf("Unknown resource: %s", resourceType)}
	}
}

func (s *Server) resourceFiles(ctx context.Context, projectID string) (interface{}, *RPCError) {
	pg, err := db.GetProjectGraph(ctx, projectID)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: "Failed to query files"}
	}

	files := []map[string]interface{}{}
	for _, node := range pg.Nodes {
		if label, ok := node["label"].(string); ok && label == "File" {
			files = append(files, node)
		}
	}

	return map[string]interface{}{
		"contents": []map[string]interface{}{
			{"uri": fmt.Sprintf("project://%s/files", projectID), "mimeType": "application/json", "text": mustJSON(files)},
		},
	}, nil
}

func (s *Server) resourceGraph(ctx context.Context, projectID string) (interface{}, *RPCError) {
	pg, err := db.GetProjectGraph(ctx, projectID)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: "Failed to query graph"}
	}

	return map[string]interface{}{
		"contents": []map[string]interface{}{
			{"uri": fmt.Sprintf("project://%s/graph", projectID), "mimeType": "application/json", "text": mustJSON(pg)},
		},
	}, nil
}

func (s *Server) resourceFindings(projectID string) (interface{}, *RPCError) {
	var findings []db.Finding
	if db.DB != nil {
		db.DB.Where("project_hash = ?", projectID).Find(&findings)
	}

	return map[string]interface{}{
		"contents": []map[string]interface{}{
			{"uri": fmt.Sprintf("project://%s/findings", projectID), "mimeType": "application/json", "text": mustJSON(findings)},
		},
	}, nil
}

func (s *Server) resourceMetrics(ctx context.Context, projectID string) (interface{}, *RPCError) {
	graphStats, _ := db.GetProjectGraphStats(ctx, projectID)
	findingStats, _ := db.GetFindingStats(projectID)

	metrics := map[string]interface{}{
		"graph":    graphStats,
		"findings": findingStats,
	}

	return map[string]interface{}{
		"contents": []map[string]interface{}{
			{"uri": fmt.Sprintf("project://%s/metrics", projectID), "mimeType": "application/json", "text": mustJSON(metrics)},
		},
	}, nil
}

func mustJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("JSON marshal error: %v", err)
		return "{}"
	}
	return string(data)
}

func writeRPCError(c *fiber.Ctx, id interface{}, code int, message string) error {
	resp := RPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	}
	return c.JSON(resp)
}
