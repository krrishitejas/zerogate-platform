package parser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/php"
	"github.com/smacker/go-tree-sitter/ruby"
)

// ASTNode represents a parsed code entity (function, class, import, variable).
type ASTNode struct {
	ID        string `json:"id"`
	Label     string `json:"label"`      // File, Function, Class, Import, Variable
	Name      string `json:"name"`
	Type      string `json:"type"`       // language extension or entity subtype
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Signature string `json:"signature,omitempty"`
}

// ASTEdge represents a relationship between two nodes.
type ASTEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"` // CONTAINS, DEFINED_IN, CALLS, IMPORTS, MEMBER_OF, EXTENDS
}

// LanguageInfo holds Tree-sitter language and its query patterns.
type LanguageInfo struct {
	Language        *sitter.Language
	FuncQuery       string
	ClassQuery      string
	ImportQuery     string
	CallQuery       string
}

// supportedLanguages maps file extensions to their Tree-sitter language config.
var supportedLanguages = map[string]*LanguageInfo{
	".go": {
		Language:  golang.GetLanguage(),
		FuncQuery: `(function_declaration name: (identifier) @name)`,
		ClassQuery: `(type_declaration (type_spec name: (type_identifier) @name type: (struct_type)))`,
		ImportQuery: `(import_spec path: (interpreted_string_literal) @path)`,
		CallQuery: `(call_expression function: (identifier) @callee)`,
	},
	".js": {
		Language:  javascript.GetLanguage(),
		FuncQuery: `[(function_declaration name: (identifier) @name) (variable_declarator name: (identifier) @name value: (arrow_function))]`,
		ClassQuery: `(class_declaration name: (identifier) @name)`,
		ImportQuery: `(import_statement source: (string) @path)`,
		CallQuery: `(call_expression function: (identifier) @callee)`,
	},
	".jsx": {
		Language:  javascript.GetLanguage(),
		FuncQuery: `[(function_declaration name: (identifier) @name) (variable_declarator name: (identifier) @name value: (arrow_function))]`,
		ClassQuery: `(class_declaration name: (identifier) @name)`,
		ImportQuery: `(import_statement source: (string) @path)`,
		CallQuery: `(call_expression function: (identifier) @callee)`,
	},
	".ts": {
		Language:  typescript.GetLanguage(),
		FuncQuery: `[(function_declaration name: (identifier) @name) (variable_declarator name: (identifier) @name value: (arrow_function))]`,
		ClassQuery: `(class_declaration name: (type_identifier) @name)`,
		ImportQuery: `(import_statement source: (string) @path)`,
		CallQuery: `(call_expression function: (identifier) @callee)`,
	},
	".tsx": {
		Language:  typescript.GetLanguage(),
		FuncQuery: `[(function_declaration name: (identifier) @name) (variable_declarator name: (identifier) @name value: (arrow_function))]`,
		ClassQuery: `(class_declaration name: (type_identifier) @name)`,
		ImportQuery: `(import_statement source: (string) @path)`,
		CallQuery: `(call_expression function: (identifier) @callee)`,
	},
	".py": {
		Language:  python.GetLanguage(),
		FuncQuery: `(function_definition name: (identifier) @name)`,
		ClassQuery: `(class_definition name: (identifier) @name)`,
		ImportQuery: `[(import_statement name: (dotted_name) @path) (import_from_statement module_name: (dotted_name) @path)]`,
		CallQuery: `(call function: (identifier) @callee)`,
	},
	".java": {
		Language:  java.GetLanguage(),
		FuncQuery: `(method_declaration name: (identifier) @name)`,
		ClassQuery: `(class_declaration name: (identifier) @name)`,
		ImportQuery: `(import_declaration (scoped_identifier) @path)`,
		CallQuery: `(method_invocation name: (identifier) @callee)`,
	},
	".rs": {
		Language:  rust.GetLanguage(),
		FuncQuery: `(function_item name: (identifier) @name)`,
		ClassQuery: `[(struct_item name: (type_identifier) @name) (impl_item type: (type_identifier) @name)]`,
		ImportQuery: `(use_declaration argument: (scoped_identifier) @path)`,
		CallQuery: `(call_expression function: (identifier) @callee)`,
	},
	".c": {
		Language:  c.GetLanguage(),
		FuncQuery: `(function_definition declarator: (function_declarator declarator: (identifier) @name))`,
		ClassQuery: `(struct_specifier name: (type_identifier) @name)`,
		ImportQuery: `(preproc_include path: (_) @path)`,
		CallQuery: `(call_expression function: (identifier) @callee)`,
	},
	".h": {
		Language:  c.GetLanguage(),
		FuncQuery: `(function_definition declarator: (function_declarator declarator: (identifier) @name))`,
		ClassQuery: `(struct_specifier name: (type_identifier) @name)`,
		ImportQuery: `(preproc_include path: (_) @path)`,
		CallQuery: `(call_expression function: (identifier) @callee)`,
	},
	".cpp": {
		Language:  cpp.GetLanguage(),
		FuncQuery: `(function_definition declarator: (function_declarator declarator: (identifier) @name))`,
		ClassQuery: `[(class_specifier name: (type_identifier) @name) (struct_specifier name: (type_identifier) @name)]`,
		ImportQuery: `(preproc_include path: (_) @path)`,
		CallQuery: `(call_expression function: (identifier) @callee)`,
	},
	".hpp": {
		Language:  cpp.GetLanguage(),
		FuncQuery: `(function_definition declarator: (function_declarator declarator: (identifier) @name))`,
		ClassQuery: `[(class_specifier name: (type_identifier) @name) (struct_specifier name: (type_identifier) @name)]`,
		ImportQuery: `(preproc_include path: (_) @path)`,
		CallQuery: `(call_expression function: (identifier) @callee)`,
	},
	".php": {
		Language:  php.GetLanguage(),
		FuncQuery: `(function_definition name: (name) @name)`,
		ClassQuery: `(class_declaration name: (name) @name)`,
		ImportQuery: `(namespace_use_declaration (namespace_use_clause (qualified_name) @path))`,
		CallQuery: `(function_call_expression function: (name) @callee)`,
	},
	".rb": {
		Language:  ruby.GetLanguage(),
		FuncQuery: `(method name: (identifier) @name)`,
		ClassQuery: `[(class name: (constant) @name) (module name: (constant) @name)]`,
		ImportQuery: `(call method: (identifier) @method arguments: (argument_list (string (string_content) @path)) (#eq? @method "require"))`,
		CallQuery: `(call method: (identifier) @callee)`,
	},
}

// IsSupportedExtension returns true if Tree-sitter can parse the given file extension.
func IsSupportedExtension(ext string) bool {
	_, ok := supportedLanguages[strings.ToLower(ext)]
	return ok
}

// ParseFileAST parses a single source file using Tree-sitter and returns extracted entities.
// projectID and relPath are used for generating stable node IDs.
func ParseFileAST(projectID, repoPath, relPath string) ([]ASTNode, []ASTEdge, error) {
	ext := strings.ToLower(filepath.Ext(relPath))
	langInfo, ok := supportedLanguages[ext]
	if !ok {
		return nil, nil, fmt.Errorf("unsupported language extension: %s", ext)
	}

	fullPath := filepath.Join(repoPath, relPath)
	sourceCode, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read file %s: %w", fullPath, err)
	}

	// Limit file size to 1MB to prevent memory issues
	if len(sourceCode) > 1024*1024 {
		return nil, nil, fmt.Errorf("file too large for AST parsing: %d bytes", len(sourceCode))
	}

	// Parse with Tree-sitter
	rootNode, err := sitter.ParseCtx(context.Background(), sourceCode, langInfo.Language)
	if err != nil {
		return nil, nil, fmt.Errorf("tree-sitter parse error for %s: %w", relPath, err)
	}

	fileID := "file:" + projectID + ":" + relPath
	var nodes []ASTNode
	var edges []ASTEdge

	// Extract functions
	funcNodes, funcEdges := extractEntities(sourceCode, rootNode, langInfo.Language, langInfo.FuncQuery, projectID, fileID, relPath, "Function")
	nodes = append(nodes, funcNodes...)
	edges = append(edges, funcEdges...)

	// Extract classes/structs
	classNodes, classEdges := extractEntities(sourceCode, rootNode, langInfo.Language, langInfo.ClassQuery, projectID, fileID, relPath, "Class")
	nodes = append(nodes, classNodes...)
	edges = append(edges, classEdges...)

	// Extract imports
	importNodes, importEdges := extractImports(sourceCode, rootNode, langInfo.Language, langInfo.ImportQuery, projectID, fileID, relPath)
	nodes = append(nodes, importNodes...)
	edges = append(edges, importEdges...)

	// Extract call relationships between functions
	callEdges := extractCalls(sourceCode, rootNode, langInfo.Language, langInfo.CallQuery, projectID, relPath, funcNodes)
	edges = append(edges, callEdges...)

	return nodes, edges, nil
}

// extractEntities runs a Tree-sitter query and creates nodes + DEFINED_IN edges.
func extractEntities(source []byte, root *sitter.Node, lang *sitter.Language, queryStr, projectID, fileID, relPath, label string) ([]ASTNode, []ASTEdge) {
	if queryStr == "" {
		return nil, nil
	}

	q, err := sitter.NewQuery([]byte(queryStr), lang)
	if err != nil {
		return nil, nil
	}
	defer q.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(q, root)

	var nodes []ASTNode
	var edges []ASTEdge
	seen := make(map[string]bool)

	for {
		match, ok := cursor.NextMatch()
		if !ok {
			break
		}

		for _, capture := range match.Captures {
			name := capture.Node.Content(source)
			startLine := int(capture.Node.StartPoint().Row) + 1
			endLine := int(capture.Node.EndPoint().Row) + 1

			// Use parent node for better line range
			parent := capture.Node.Parent()
			if parent != nil {
				startLine = int(parent.StartPoint().Row) + 1
				endLine = int(parent.EndPoint().Row) + 1
			}

			nodeID := fmt.Sprintf("%s:%s:%s:%s", strings.ToLower(label), projectID, relPath, name)
			if seen[nodeID] {
				continue
			}
			seen[nodeID] = true

			nodes = append(nodes, ASTNode{
				ID:        nodeID,
				Label:     label,
				Name:      name,
				Type:      filepath.Ext(relPath),
				LineStart: startLine,
				LineEnd:   endLine,
			})

			edges = append(edges, ASTEdge{
				Source: nodeID,
				Target: fileID,
				Type:   "DEFINED_IN",
			})
		}
	}

	return nodes, edges
}

// extractImports extracts import statements and creates IMPORTS edges.
func extractImports(source []byte, root *sitter.Node, lang *sitter.Language, queryStr, projectID, fileID, relPath string) ([]ASTNode, []ASTEdge) {
	if queryStr == "" {
		return nil, nil
	}

	q, err := sitter.NewQuery([]byte(queryStr), lang)
	if err != nil {
		return nil, nil
	}
	defer q.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(q, root)

	var nodes []ASTNode
	var edges []ASTEdge
	seen := make(map[string]bool)

	for {
		match, ok := cursor.NextMatch()
		if !ok {
			break
		}

		for _, capture := range match.Captures {
			importPath := capture.Node.Content(source)
			// Clean up quotes
			importPath = strings.Trim(importPath, "\"'`<>")

			nodeID := fmt.Sprintf("import:%s:%s:%s", projectID, relPath, importPath)
			if seen[nodeID] {
				continue
			}
			seen[nodeID] = true

			nodes = append(nodes, ASTNode{
				ID:    nodeID,
				Label: "Import",
				Name:  importPath,
				Type:  "dependency",
			})

			edges = append(edges, ASTEdge{
				Source: fileID,
				Target: nodeID,
				Type:   "IMPORTS",
			})
		}
	}

	return nodes, edges
}

// extractCalls finds function calls and maps them to known function nodes.
func extractCalls(source []byte, root *sitter.Node, lang *sitter.Language, queryStr, projectID, relPath string, knownFunctions []ASTNode) []ASTEdge {
	if queryStr == "" || len(knownFunctions) == 0 {
		return nil
	}

	q, err := sitter.NewQuery([]byte(queryStr), lang)
	if err != nil {
		return nil
	}
	defer q.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(q, root)

	// Build a lookup of known function names to their IDs
	funcLookup := make(map[string]string)
	for _, fn := range knownFunctions {
		funcLookup[fn.Name] = fn.ID
	}

	var edges []ASTEdge
	seen := make(map[string]bool)

	for {
		match, ok := cursor.NextMatch()
		if !ok {
			break
		}

		for _, capture := range match.Captures {
			calleeName := capture.Node.Content(source)

			// Check if this call targets a known function in the same file
			if targetID, exists := funcLookup[calleeName]; exists {
				// Determine which function this call is inside of
				callerNode := findEnclosingFunction(capture.Node, source)
				if callerNode != "" {
					callerID := fmt.Sprintf("function:%s:%s:%s", projectID, relPath, callerNode)
					edgeKey := callerID + "->" + targetID
					if !seen[edgeKey] && callerID != targetID {
						seen[edgeKey] = true
						edges = append(edges, ASTEdge{
							Source: callerID,
							Target: targetID,
							Type:   "CALLS",
						})
					}
				}
			}
		}
	}

	return edges
}

// findEnclosingFunction walks up the AST to find the function containing this node.
func findEnclosingFunction(node *sitter.Node, source []byte) string {
	current := node.Parent()
	for current != nil {
		nodeType := current.Type()
		switch nodeType {
		case "function_declaration", "function_definition", "method_declaration",
			"function_item", "method":
			// Look for the name child
			for i := 0; i < int(current.ChildCount()); i++ {
				child := current.Child(int(i))
				if child != nil && (child.Type() == "identifier" || child.Type() == "name" || child.Type() == "type_identifier") {
					return child.Content(source)
				}
				// For declarators (C/C++)
				if child != nil && child.Type() == "function_declarator" {
					for j := 0; j < int(child.ChildCount()); j++ {
						grandchild := child.Child(int(j))
						if grandchild != nil && grandchild.Type() == "identifier" {
							return grandchild.Content(source)
						}
					}
				}
			}
		}
		current = current.Parent()
	}
	return ""
}
