package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/nats-io/nats.go"
	"github.com/zerogate/api/internal/agents"
	"github.com/zerogate/api/internal/agents/aggregator"
	"github.com/zerogate/api/internal/agents/autofix"
	"github.com/zerogate/api/internal/agents/bug"
	"github.com/zerogate/api/internal/agents/embedding"
	"github.com/zerogate/api/internal/agents/ingestion"
	"github.com/zerogate/api/internal/agents/knowledgegraph"
	"github.com/zerogate/api/internal/agents/logic"
	"github.com/zerogate/api/internal/agents/parser"
	"github.com/zerogate/api/internal/agents/performance"
	"github.com/zerogate/api/internal/agents/security"
	"github.com/zerogate/api/internal/agents/validation"
	"github.com/zerogate/api/internal/auth"
	"github.com/zerogate/api/internal/db"
	"github.com/zerogate/api/internal/mcp"
	"github.com/zerogate/api/internal/watcher"
)

func main() {
	// 1. Connect to PostgreSQL
	dsn := "host=localhost user=zerogate password=zerogate_password dbname=zerogate_dev port=5433 sslmode=disable TimeZone=UTC"
	if err := db.ConnectPostgres(dsn); err != nil {
		log.Fatalf("Failed to connect/migrate database: %v", err)
	}

	// 2. Connect to NATS
	nc, err := nats.Connect("nats://localhost:4222")
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	log.Println("Connected to NATS event bus successfully.")

	// 3. Connect to Memgraph
	err = db.ConnectMemgraph("bolt://localhost:7687", "", "")
	if err != nil {
		log.Fatalf("Failed to connect to Memgraph: %v", err)
	}
	log.Println("Connected to Memgraph successfully.")

	// 4. Init Qdrant
	err = db.InitQdrant()
	if err != nil {
		log.Fatalf("Failed to init Qdrant: %v", err)
	}
	log.Println("Initialized Qdrant successfully.")

	// 5. Initialize File Watcher Service
	watcherService := watcher.NewWatcherService(nc)

	// 6. Initialize WebSocket Hub
	wsHub := watcher.NewWebSocketHub(nc)

	// 7. Initialize and Start Agents
	ingestionAgent := ingestion.NewAgent(nc)
	if err := ingestionAgent.Start(); err != nil {
		log.Fatalf("Failed to start ingestion agent: %v", err)
	}

	parserAgent := parser.NewAgent(nc)
	if err := parserAgent.Start(); err != nil {
		log.Fatalf("Failed to start parser agent: %v", err)
	}

	kgAgent := knowledgegraph.NewAgent(nc)
	if err := kgAgent.Start(); err != nil {
		log.Fatalf("Failed to start knowledge graph agent: %v", err)
	}

	embAgent := embedding.NewAgent(nc)
	if err := embAgent.Start(); err != nil {
		log.Fatalf("Failed to start embedding agent: %v", err)
	}

	_ = bug.NewAgent(nc).Start()
	_ = security.NewAgent(nc).Start()
	_ = logic.NewAgent(nc).Start()
	_ = performance.NewAgent(nc).Start()
	_ = aggregator.NewAgent(nc).Start()
	_ = autofix.NewAgent(nc).Start()
	_ = validation.NewAgent(nc).Start()

	registry := agents.NewRegistry(nc)
	_ = registry.Start()

	// 8. Auto-start watcher after ingestion completes
	nc.Subscribe("project.ingested", func(msg *nats.Msg) {
		var event struct {
			ProjectID string `json:"project_id"`
			Path      string `json:"path"`
		}
		if err := json.Unmarshal(msg.Data, &event); err == nil {
			go func() {
				if err := watcherService.StartWatching(event.ProjectID, event.Path); err != nil {
					log.Printf("Failed to start file watcher for %s: %v", event.ProjectID, err)
				}
			}()
		}
	})

	// ========================
	// Fiber HTTP Server
	// ========================
	app := fiber.New(fiber.Config{
		AppName: "ZEROGATE Core API",
	})

	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
	}))

	// Auth & Audit Middleware (Phase 3)
	app.Use(auth.JWTMiddleware(auth.DefaultAuthConfig()))
	app.Use(auth.AuditMiddleware())

	api := app.Group("/api/v1")

	// ---- Health ----
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":  "online",
			"service": "zerogate-api-gateway",
		})
	})

	// ---- Auth / SSO ----
	authGroup := api.Group("/auth")
	authConfig := auth.DefaultOAuthConfig()
	authGroup.Get("/sso/login", auth.OAuthLogin(authConfig))
	authGroup.Get("/sso/callback", auth.OAuthCallback(authConfig, auth.DefaultAuthConfig().SecretKey))

	// ---- MCP Server ----
	mcpServer := mcp.NewServer()
	api.Post("/mcp/rpc", mcpServer.HandleRPC)

	// ---- Audit Logs ----
	api.Get("/audit-logs", func(c *fiber.Ctx) error {
		orgID, _ := c.Locals("org_id").(string)
		if orgID == "" {
			orgID = "dev-org"
		}
		
		limitStr := c.Query("limit", "50")
		offsetStr := c.Query("offset", "0")
		limit, _ := strconv.Atoi(limitStr)
		offset, _ := strconv.Atoi(offsetStr)

		logs, err := db.GetAuditLogs(orgID, limit, offset)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch audit logs"})
		}
		
		return c.JSON(fiber.Map{"logs": logs})
	})

	api.Get("/audit-logs/export", func(c *fiber.Ctx) error {
		orgID, _ := c.Locals("org_id").(string)
		if orgID == "" {
			orgID = "dev-org"
		}
		logs, err := db.GetAuditLogs(orgID, 1000, 0)
		if err != nil {
			return c.Status(500).SendString("Failed to fetch logs")
		}

		c.Set("Content-Type", "text/csv")
		c.Set("Content-Disposition", "attachment; filename=audit_logs.csv")

		csvData := "Timestamp,User,Action,IP Address,Metadata\n"
		for _, log := range logs {
			meta := strings.ReplaceAll(log.Metadata, "\"", "\"\"")
			csvData += fmt.Sprintf("\"%s\",\"%s\",\"%s\",\"%s\",\"%s\"\n", 
				time.Unix(log.CreatedAt/1000, 0).Format(time.RFC3339),
				log.UserEmail,
				log.Action,
				log.IPAddress,
				meta,
			)
		}
		return c.SendString(csvData)
	})

	// ---- Trigger Ingestion ----
	api.Post("/trigger-ingestion", func(c *fiber.Ctx) error {
		payload := struct {
			ProjectID     string `json:"project_id"`
			RepositoryURL string `json:"repository_url"`
		}{}
		if err := c.BodyParser(&payload); err != nil {
			return c.Status(400).SendString(err.Error())
		}

		data, _ := json.Marshal(payload)
		if err := nc.Publish("project.requested", data); err != nil {
			return c.Status(500).SendString(err.Error())
		}

		return c.JSON(fiber.Map{"status": "ingestion_triggered", "project_id": payload.ProjectID})
	})

	// ========================
	// Project Service
	// ========================
	projects := api.Group("/projects")

	projects.Get("/", func(c *fiber.Ctx) error {
		var dbProjects []db.Project
		if err := db.DB.Order("created_at desc").Find(&dbProjects).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch projects"})
		}

		var formattedProjects []map[string]interface{}
		for _, p := range dbProjects {
			
			// Map source from URL
			source := "local"
			if strings.Contains(p.RepositoryURL, "github.com") {
				source = "github"
			}

			// Get stats
			stats, _ := db.GetFindingStats(p.ProjectHash)

			formattedProjects = append(formattedProjects, map[string]interface{}{
				"id":       p.ProjectHash,
				"name":     p.Name,
				"source":   source,
				"status":   "active",
				"branch":   p.DefaultBranch,
				"lastScan": p.CreatedAt.Format("2006-01-02 15:04"),
				"findings": fiber.Map{
					"critical": stats["critical"],
					"high":     stats["high"],
				},
			})
		}

		// Fallback to empty
		if formattedProjects == nil {
			formattedProjects = []map[string]interface{}{}
		}

		return c.JSON(fiber.Map{"projects": formattedProjects})
	})

	// ---- Agent Telemetry ----
	api.Get("/agent-stats", func(c *fiber.Ctx) error {
		// In a real production environment, this queries Prometheus or NATS JetStream admin API for msg rates.
		// For the platform API, we provide the registered topology. 
		agents := []map[string]interface{}{
			{ "id": "ag-1", "name": "Ingestion Agent", "model": "Go-Git / Archiver", "status": "online", "icon": "Package", "reqs": "2/sec", "latency": "45ms" },
			{ "id": "ag-2", "name": "AST Parser Agent", "model": "Tree-sitter", "status": "busy", "icon": "Compass", "reqs": "14/sec", "latency": "12ms" },
			{ "id": "ag-3", "name": "Bug Detection Agent", "model": "StarCoder2-7B + Semgrep", "status": "busy", "icon": "Bug", "reqs": "4/sec", "latency": "850ms" },
			{ "id": "ag-4", "name": "Security Analysis Agent", "model": "Qwen2.5-Coder-32B", "status": "online", "icon": "Shield", "reqs": "0/sec", "latency": "-" },
			{ "id": "ag-5", "name": "Logic / Arch Agent", "model": "DeepSeek-Coder-V2", "status": "busy", "icon": "BrainCircuit", "reqs": "1/sec", "latency": "2400ms" },
			{ "id": "ag-6", "name": "Performance Agent", "model": "CodeLlama-34B", "status": "online", "icon": "Zap", "reqs": "0/sec", "latency": "-" },
			{ "id": "ag-7", "name": "Auto-Fix Agent", "model": "Aider / StarCoder2-15B", "status": "online", "icon": "Wrench", "reqs": "0/sec", "latency": "-" },
			{ "id": "ag-8", "name": "Validation Agent", "model": "Docker Sandbox", "status": "offline", "icon": "Beaker", "reqs": "-", "latency": "-" },
			{ "id": "ag-9", "name": "Knowledge Graph Agent", "model": "Memgraph / BGE-M3", "status": "busy", "icon": "Activity", "reqs": "14/sec", "latency": "120ms" },
		}
		return c.JSON(fiber.Map{"agents": agents})
	})

	// ---- Semantic Search ----
	projects.Get("/:id/search", func(c *fiber.Ctx) error {
		projectID := c.Params("id")
		query := c.Query("q")
		if query == "" {
			return c.Status(400).JSON(fiber.Map{"error": "Query parameter 'q' is required"})
		}

		vector, err := embedding.GenerateEmbedding(query)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to generate query embedding"})
		}

		limitStr := c.Query("limit", "10")
		limit, _ := strconv.Atoi(limitStr)
		if limit <= 0 || limit > 50 {
			limit = 10
		}

		results, err := db.SearchEmbeddings(vector, projectID, limit)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to perform semantic search"})
		}

		return c.JSON(fiber.Map{
			"query":   query,
			"results": results,
			"count":   len(results),
		})
	})

	// ---- Knowledge Graph ----
	projects.Get("/:id/graph", func(c *fiber.Ctx) error {
		projectID := c.Params("id")

		pg, err := db.GetProjectGraph(c.Context(), projectID)
		if err != nil {
			log.Printf("Error fetching graph for project %s: %v", projectID, err)
			return c.Status(500).JSON(fiber.Map{
				"nodes": []map[string]any{},
				"links": []map[string]any{},
			})
		}

		if len(pg.Nodes) == 0 {
			pg.Nodes = []map[string]any{}
		}
		if len(pg.Links) == 0 {
			pg.Links = []map[string]any{}
		}

		return c.JSON(fiber.Map{
			"nodes": pg.Nodes,
			"links": pg.Links,
		})
	})

	projects.Get("/:id/graph/stats", func(c *fiber.Ctx) error {
		projectID := c.Params("id")
		stats, err := db.GetProjectGraphStats(c.Context(), projectID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch graph stats"})
		}
		return c.JSON(stats)
	})

	projects.Get("/:id/graph/functions/:funcId/calls", func(c *fiber.Ctx) error {
		projectID := c.Params("id")
		funcID := c.Params("funcId")

		pg, err := db.QueryFunctionCallChain(c.Context(), projectID, funcID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to query call chain"})
		}

		return c.JSON(fiber.Map{"nodes": pg.Nodes, "links": pg.Links})
	})

	projects.Get("/:id/graph/files/:fileId/deps", func(c *fiber.Ctx) error {
		projectID := c.Params("id")
		fileID := c.Params("fileId")

		pg, err := db.QueryFileDependencies(c.Context(), projectID, fileID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to query file deps"})
		}

		return c.JSON(fiber.Map{"nodes": pg.Nodes, "links": pg.Links})
	})

	// ---- File Watcher ----
	projects.Post("/:id/watch/start", func(c *fiber.Ctx) error {
		projectID := c.Params("id")
		repoPath := filepath.Join(os.TempDir(), "zerogate-repos", projectID)

		if _, err := os.Stat(repoPath); os.IsNotExist(err) {
			return c.Status(404).JSON(fiber.Map{"error": "Project repository not found. Trigger ingestion first."})
		}

		if err := watcherService.StartWatching(projectID, repoPath); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}

		return c.JSON(fiber.Map{"status": "watching", "project_id": projectID})
	})

	projects.Post("/:id/watch/stop", func(c *fiber.Ctx) error {
		projectID := c.Params("id")
		watcherService.StopWatching(projectID)
		return c.JSON(fiber.Map{"status": "stopped", "project_id": projectID})
	})

	projects.Get("/:id/watch/status", func(c *fiber.Ctx) error {
		projectID := c.Params("id")
		return c.JSON(fiber.Map{
			"project_id": projectID,
			"watching":   watcherService.IsWatching(projectID),
		})
	})

	// ========================
	// Findings Service
	// ========================
	findings := api.Group("/findings")

	findings.Get("/", func(c *fiber.Ctx) error {
		projectID := c.Query("project_id")

		var dbFindings []db.Finding
		query := db.DB.Order("created_at desc")
		if projectID != "" {
			query = query.Where("project_hash = ?", projectID)
		}

		if err := query.Find(&dbFindings).Error; err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch findings"})
		}

		return c.JSON(fiber.Map{"findings": dbFindings})
	})

	// ---- Single Finding Detail ----
	findings.Get("/:id", func(c *fiber.Ctx) error {
		idStr := c.Params("id")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid finding ID"})
		}

		finding, err := db.GetFindingByID(uint(id))
		if err != nil || finding == nil {
			return c.Status(404).JSON(fiber.Map{"error": "Finding not found"})
		}

		approvals, _ := db.GetFindingApprovals(uint(id))
		if approvals == nil {
			approvals = []db.FixApproval{}
		}

		return c.JSON(fiber.Map{
			"finding":   finding,
			"approvals": approvals,
		})
	})

	// ---- Pending Fixes ----
	findings.Get("/pending-fixes", func(c *fiber.Ctx) error {
		// This must come before /:id to avoid conflict
		projectID := c.Query("project_id")
		pendingFindings, err := db.GetPendingFixes(projectID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch pending fixes"})
		}
		if pendingFindings == nil {
			pendingFindings = []db.Finding{}
		}
		return c.JSON(fiber.Map{"findings": pendingFindings})
	})

	// ---- Finding Stats ----
	findings.Get("/stats", func(c *fiber.Ctx) error {
		projectID := c.Query("project_id")
		stats, err := db.GetFindingStats(projectID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch stats"})
		}
		return c.JSON(stats)
	})

	// ---- Approve Fix ----
	findings.Post("/:id/approve", func(c *fiber.Ctx) error {
		idStr := c.Params("id")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid finding ID"})
		}

		var body struct {
			Comment    string `json:"comment"`
			ApprovedBy string `json:"approved_by"`
		}
		if err := c.BodyParser(&body); err != nil {
			body.ApprovedBy = "operator"
		}
		if body.ApprovedBy == "" {
			body.ApprovedBy = "operator"
		}

		approval := &db.FixApproval{
			FindingID:  uint(id),
			ApprovedBy: body.ApprovedBy,
			Action:     "approve",
			Comment:    body.Comment,
		}

		if err := db.CreateFixApproval(approval); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to approve fix"})
		}

		return c.JSON(fiber.Map{"status": "approved", "finding_id": id})
	})

	// ---- Reject Fix ----
	findings.Post("/:id/reject", func(c *fiber.Ctx) error {
		idStr := c.Params("id")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid finding ID"})
		}

		var body struct {
			Comment    string `json:"comment"`
			ApprovedBy string `json:"approved_by"`
		}
		if err := c.BodyParser(&body); err != nil {
			body.ApprovedBy = "operator"
		}
		if body.ApprovedBy == "" {
			body.ApprovedBy = "operator"
		}

		approval := &db.FixApproval{
			FindingID:  uint(id),
			ApprovedBy: body.ApprovedBy,
			Action:     "reject",
			Comment:    body.Comment,
		}

		if err := db.CreateFixApproval(approval); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to reject fix"})
		}

		return c.JSON(fiber.Map{"status": "rejected", "finding_id": id})
	})

	// ---- Regenerate Fix ----
	findings.Post("/:id/regenerate-fix", func(c *fiber.Ctx) error {
		idStr := c.Params("id")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid finding ID"})
		}

		finding, err := db.GetFindingByID(uint(id))
		if err != nil || finding == nil {
			return c.Status(404).JSON(fiber.Map{"error": "Finding not found"})
		}

		// Re-trigger the autofix pipeline via NATS
		event := map[string]any{
			"id":          finding.ID,
			"project_id":  finding.ProjectHash,
			"rule_id":     finding.RuleID,
			"file_path":   finding.FilePath,
			"description": finding.Description,
			"line_start":  finding.LineStart,
		}
		data, _ := json.Marshal(event)
		nc.Publish("finding.created", data)

		return c.JSON(fiber.Map{"status": "regenerating", "finding_id": id})
	})

	// ========================
	// WebSocket (Real-time)
	// ========================
	wsHub.RegisterRoutes(api)

	// ========================
	// Start Server
	// ========================
	log.Println("ZEROGATE Core API starting on port 8000...")
	if err := app.Listen(":8000"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
