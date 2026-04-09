package db

import (
	"context"
	"fmt"
	"log"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

var Driver neo4j.DriverWithContext

func ConnectMemgraph(uri, user, pass string) error {
	var err error
	Driver, err = neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, pass, ""))
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := Driver.VerifyConnectivity(ctx); err != nil {
		return err
	}

	// Create indexes for performance
	session := Driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	indexes := []string{
		"CREATE INDEX ON :Entity(id)",
		"CREATE INDEX ON :Entity(project_id)",
		"CREATE INDEX ON :Entity(label)",
	}
	for _, idx := range indexes {
		_, _ = session.Run(ctx, idx, nil)
	}

	return nil
}

// GraphNode represents a generic node from the parser
type GraphNode struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// GraphEdge represents a relationship between nodes
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// SaveNodesAndEdges saves the parser output nodes and edges into Memgraph.
// Uses dynamic labels stored as properties and typed relationships.
func SaveNodesAndEdges(ctx context.Context, projectID string, nodes []GraphNode, edges []GraphEdge) error {
	session := Driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		// Insert Nodes with all metadata
		nodeQuery := `
			UNWIND $nodes AS n
			MERGE (e:Entity {id: n.id})
			SET e.project_id = $projectId,
			    e.label = n.label,
			    e.name = n.name,
			    e.type = n.type,
			    e.line_start = n.line_start,
			    e.line_end = n.line_end,
			    e.signature = n.signature
		`

		nodeMaps := make([]map[string]any, len(nodes))
		for i, n := range nodes {
			nodeMaps[i] = map[string]any{
				"id":         n.ID,
				"label":      n.Label,
				"name":       n.Name,
				"type":       n.Type,
				"line_start": n.LineStart,
				"line_end":   n.LineEnd,
				"signature":  n.Signature,
			}
		}

		_, err := tx.Run(ctx, nodeQuery, map[string]any{
			"nodes":     nodeMaps,
			"projectId": projectID,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to insert nodes: %w", err)
		}

		// Insert edges with typed relationship properties
		edgeQuery := `
			UNWIND $edges AS rel
			MATCH (source:Entity {id: rel.source})
			MATCH (target:Entity {id: rel.target})
			MERGE (source)-[r:REL {project_id: $projectId, type: rel.type}]->(target)
		`

		edgeMaps := make([]map[string]any, len(edges))
		for i, e := range edges {
			edgeMaps[i] = map[string]any{
				"source": e.Source,
				"target": e.Target,
				"type":   e.Type,
			}
		}

		_, err = tx.Run(ctx, edgeQuery, map[string]any{
			"edges":     edgeMaps,
			"projectId": projectID,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to insert edges: %w", err)
		}

		return nil, nil
	})

	return err
}

// DeleteProjectGraph removes all nodes and edges for a project (for incremental re-analysis).
func DeleteProjectGraph(ctx context.Context, projectID string) error {
	session := Driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		query := `
			MATCH (n:Entity {project_id: $projectId})
			DETACH DELETE n
		`
		_, err := tx.Run(ctx, query, map[string]any{"projectId": projectID})
		return nil, err
	})

	return err
}

// DeleteFileSubgraph removes graph nodes belonging to a specific file (for incremental updates).
func DeleteFileSubgraph(ctx context.Context, projectID, fileID string) error {
	session := Driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		// Delete entities defined in this file and their relationships
		query := `
			MATCH (entity:Entity)-[:REL {type: 'DEFINED_IN'}]->(file:Entity {id: $fileId, project_id: $projectId})
			DETACH DELETE entity
		`
		_, err := tx.Run(ctx, query, map[string]any{
			"fileId":    fileID,
			"projectId": projectID,
		})
		return nil, err
	})

	return err
}

// ProjectGraph represents the output requested by the frontend
type ProjectGraph struct {
	Nodes []map[string]any `json:"nodes"`
	Links []map[string]any `json:"links"`
}

// GetProjectGraph returns the graph format required by the Next.js frontend D3/ForceGraph
func GetProjectGraph(ctx context.Context, projectID string) (ProjectGraph, error) {
	session := Driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	var pg ProjectGraph

	_, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		// Read nodes
		nodeQuery := `MATCH (n:Entity {project_id: $projectId}) RETURN n.id AS id, n.name AS name, n.label AS label, n.type AS type, n.line_start AS line_start, n.line_end AS line_end`
		nodeRes, err := tx.Run(ctx, nodeQuery, map[string]any{"projectId": projectID})
		if err != nil {
			return nil, err
		}

		nodes := []map[string]any{}
		for nodeRes.Next(ctx) {
			rec := nodeRes.Record()
			id, _ := rec.Get("id")
			name, _ := rec.Get("name")
			label, _ := rec.Get("label")

			// Compute group for frontend coloration based on entity type
			group := 2
			lblStr, _ := label.(string)
			switch lblStr {
			case "Project":
				group = 1
			case "File":
				group = 2
			case "Function":
				group = 3
			case "Class":
				group = 4
			case "Import":
				group = 5
			case "Variable":
				group = 6
			}

			// Size based on entity type
			val := 3
			switch lblStr {
			case "Project":
				val = 8
			case "File":
				val = 5
			case "Function":
				val = 3
			case "Class":
				val = 4
			}

			nodes = append(nodes, map[string]any{
				"id":    id,
				"name":  name,
				"group": group,
				"label": label,
				"val":   val,
			})
		}
		if err := nodeRes.Err(); err != nil {
			return nil, err
		}
		pg.Nodes = nodes

		// Read edges
		edgeQuery := `
			MATCH (s:Entity {project_id: $projectId})-[r:REL]->(t:Entity {project_id: $projectId})
			RETURN s.id AS source, t.id AS target, r.type AS label
		`
		edgeRes, err := tx.Run(ctx, edgeQuery, map[string]any{"projectId": projectID})
		if err != nil {
			return nil, err
		}

		links := []map[string]any{}
		for edgeRes.Next(ctx) {
			rec := edgeRes.Record()
			source, _ := rec.Get("source")
			target, _ := rec.Get("target")
			lbl, _ := rec.Get("label")

			links = append(links, map[string]any{
				"source": source,
				"target": target,
				"label":  lbl,
			})
		}
		if err := edgeRes.Err(); err != nil {
			return nil, err
		}
		pg.Links = links

		return nil, nil
	})

	return pg, err
}

// QueryFunctionCallChain returns the call graph for a specific function.
func QueryFunctionCallChain(ctx context.Context, projectID, functionID string) (ProjectGraph, error) {
	session := Driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	var pg ProjectGraph

	_, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		query := `
			MATCH path = (start:Entity {id: $funcId, project_id: $projectId})-[:REL*1..5 {type: 'CALLS'}]->(callee:Entity)
			WITH nodes(path) AS ns, relationships(path) AS rs
			UNWIND ns AS n
			WITH COLLECT(DISTINCT {id: n.id, name: n.name, label: n.label, group: 3, val: 3}) AS nodes, rs
			UNWIND rs AS r
			WITH nodes, COLLECT(DISTINCT {source: startNode(r).id, target: endNode(r).id, label: r.type}) AS links
			RETURN nodes, links
		`
		res, err := tx.Run(ctx, query, map[string]any{
			"funcId":    functionID,
			"projectId": projectID,
		})
		if err != nil {
			return nil, err
		}

		if res.Next(ctx) {
			rec := res.Record()
			if nodeList, ok := rec.Get("nodes"); ok {
				if nodes, ok := nodeList.([]any); ok {
					for _, n := range nodes {
						if m, ok := n.(map[string]any); ok {
							pg.Nodes = append(pg.Nodes, m)
						}
					}
				}
			}
			if linkList, ok := rec.Get("links"); ok {
				if links, ok := linkList.([]any); ok {
					for _, l := range links {
						if m, ok := l.(map[string]any); ok {
							pg.Links = append(pg.Links, m)
						}
					}
				}
			}
		}

		return nil, nil
	})

	if pg.Nodes == nil {
		pg.Nodes = []map[string]any{}
	}
	if pg.Links == nil {
		pg.Links = []map[string]any{}
	}

	return pg, err
}

// QueryFileDependencies returns the import/dependency graph for a specific file.
func QueryFileDependencies(ctx context.Context, projectID, fileID string) (ProjectGraph, error) {
	session := Driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	var pg ProjectGraph

	_, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		query := `
			MATCH (file:Entity {id: $fileId, project_id: $projectId})
			OPTIONAL MATCH (file)-[r:REL {type: 'IMPORTS'}]->(dep:Entity)
			OPTIONAL MATCH (entity:Entity)-[d:REL {type: 'DEFINED_IN'}]->(file)
			WITH file, COLLECT(DISTINCT dep) AS deps, COLLECT(DISTINCT entity) AS entities,
			     COLLECT(DISTINCT r) AS depRels, COLLECT(DISTINCT d) AS defRels
			RETURN file, deps, entities, depRels, defRels
		`
		res, err := tx.Run(ctx, query, map[string]any{
			"fileId":    fileID,
			"projectId": projectID,
		})
		if err != nil {
			return nil, err
		}

		nodes := []map[string]any{}
		links := []map[string]any{}

		if res.Next(ctx) {
			rec := res.Record()

			// Add the file node
			if fileNode, ok := rec.Get("file"); ok {
				if n, ok := fileNode.(neo4j.Node); ok {
					props := n.Props
					nodes = append(nodes, map[string]any{
						"id": props["id"], "name": props["name"], "label": "File", "group": 2, "val": 5,
					})
				}
			}

			// Add dependencies
			if depList, ok := rec.Get("deps"); ok {
				if deps, ok := depList.([]any); ok {
					for _, d := range deps {
						if n, ok := d.(neo4j.Node); ok {
							props := n.Props
							nodes = append(nodes, map[string]any{
								"id": props["id"], "name": props["name"], "label": props["label"], "group": 5, "val": 2,
							})
							links = append(links, map[string]any{
								"source": rec.Values[0].(neo4j.Node).Props["id"],
								"target": props["id"],
								"label":  "IMPORTS",
							})
						}
					}
				}
			}
		}

		pg.Nodes = nodes
		pg.Links = links
		return nil, nil
	})

	if pg.Nodes == nil {
		pg.Nodes = []map[string]any{}
	}
	if pg.Links == nil {
		pg.Links = []map[string]any{}
	}

	return pg, err
}

// GetProjectGraphStats returns high-level statistics about the project's knowledge graph.
func GetProjectGraphStats(ctx context.Context, projectID string) (map[string]int, error) {
	session := Driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	stats := map[string]int{
		"total_nodes": 0,
		"files":       0,
		"functions":   0,
		"classes":     0,
		"imports":     0,
		"edges":       0,
	}

	_, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		query := `
			MATCH (n:Entity {project_id: $projectId})
			RETURN n.label AS label, count(n) AS cnt
		`
		res, err := tx.Run(ctx, query, map[string]any{"projectId": projectID})
		if err != nil {
			return nil, err
		}

		total := 0
		for res.Next(ctx) {
			rec := res.Record()
			label, _ := rec.Get("label")
			cnt, _ := rec.Get("cnt")
			lblStr, _ := label.(string)
			cntInt, _ := cnt.(int64)

			total += int(cntInt)
			switch lblStr {
			case "File":
				stats["files"] = int(cntInt)
			case "Function":
				stats["functions"] = int(cntInt)
			case "Class":
				stats["classes"] = int(cntInt)
			case "Import":
				stats["imports"] = int(cntInt)
			}
		}
		stats["total_nodes"] = total

		// Count edges
		edgeQuery := `
			MATCH (:Entity {project_id: $projectId})-[r:REL]->(:Entity)
			RETURN count(r) AS cnt
		`
		edgeRes, err := tx.Run(ctx, edgeQuery, map[string]any{"projectId": projectID})
		if err != nil {
			return nil, err
		}
		if edgeRes.Next(ctx) {
			rec := edgeRes.Record()
			cnt, _ := rec.Get("cnt")
			if c, ok := cnt.(int64); ok {
				stats["edges"] = int(c)
			}
		}

		return nil, nil
	})

	log.Printf("Graph stats for project %s: %v", projectID, stats)
	return stats, err
}
