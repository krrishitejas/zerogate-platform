package scanner

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/zerogate/api/internal/llm"
)

type FindingResult struct {
	RuleID      string
	Severity    string
	Title       string
	Description string
	LineStart   int
	LineEnd     int
	Category    string
	CweID       string
	CvssScore   float64
	RootCause   string
	Impact      string
	Confidence  float64
	Source      string  // "regex" or "llm"
}

var bugRules = []struct {
	RuleID      string
	Title       string
	Description string
	Severity    string
	Pattern     *regexp.Regexp
}{
	{"BUG-001", "Unhandled Error Pattern", "Detected a function call returning an error that is explicitly ignored or poorly handled.", "high", regexp.MustCompile(`_\s*,\s*err\s*:=`)},
	{"BUG-002", "Potential Nil Pointer", "Possible nil pointer dereference without checking.", "medium", regexp.MustCompile(`(?i)(?:nil)?\.\w+\(`)},
	{"BUG-003", "Unused Variable Assignment", "Variable assigned but potentially never used.", "low", regexp.MustCompile(`\w+\s*:=\s*.*//\s*nolint`)},
	{"BUG-004", "Empty Error Check", "Error is checked but the error handling body is empty.", "medium", regexp.MustCompile(`if\s+err\s*!=\s*nil\s*\{\s*\}`)},
}

var secRules = []struct {
	RuleID      string
	Title       string
	Description string
	Severity    string
	CweID       string
	Pattern     *regexp.Regexp
}{
	{"SEC-001", "SQL Injection Risk", "Raw string interpolation detected in query. Use parameterized queries.", "critical", "CWE-89", regexp.MustCompile(`fmt\.Sprintf\(.*SELECT.*%s`)},
	{"SEC-002", "Hardcoded Secret", "Found potential hardcoded secret/API key.", "critical", "CWE-798", regexp.MustCompile(`(?i)(api_key|password|secret)\s*=\s*["'][a-zA-Z0-9]{10,}["']`)},
	{"SEC-003", "Weak RNG", "Usage of non-cryptographic RNG.", "medium", "CWE-338", regexp.MustCompile(`math/rand\.`)},
	{"SEC-004", "Path Traversal Risk", "User input used in file path without sanitization.", "high", "CWE-22", regexp.MustCompile(`os\.(Open|ReadFile|Create)\(\s*\w+\s*\+`)},
	{"SEC-005", "Command Injection Risk", "Shell command constructed with user input.", "critical", "CWE-78", regexp.MustCompile(`exec\.Command\(\s*.*\+`)},
	{"SEC-006", "Insecure TLS Config", "TLS certificate verification disabled.", "high", "CWE-295", regexp.MustCompile(`InsecureSkipVerify:\s*true`)},
}

var perfRules = []struct {
	RuleID      string
	Title       string
	Description string
	Severity    string
	Pattern     *regexp.Regexp
}{
	{"PERF-001", "Sleep block usage", "Using time.Sleep or similar blocking construct. Consider contexts/channels.", "medium", regexp.MustCompile(`time\.Sleep\(`)},
	{"PERF-002", "String Concatenation in Loop", "Repeated string concatenation in a loop is inefficient. Use strings.Builder.", "medium", regexp.MustCompile(`for\s.*\{[^}]*\+\s*=\s*"`)},
	{"PERF-003", "Unbuffered Channel", "Unbuffered channel may cause goroutine blocking.", "low", regexp.MustCompile(`make\(chan\s+\w+\)`)},
}

var logicRules = []struct {
	RuleID      string
	Title       string
	Description string
	Severity    string
	Pattern     *regexp.Regexp
}{
	{"ARCH-001", "God Object Anti-Pattern Detected", "This structural component may be doing too much. Consider Single Responsibility Principle.", "low", regexp.MustCompile(`type\s+Manager\s+struct`)},
	{"ARCH-002", "Tight Coupling Anti-Pattern", "Direct instantiation of abstract entity; should inject dependencies.", "medium", regexp.MustCompile(`=\s*&?[A-Z][a-zA-Z0-9_]*Impl\{`)},
	{"ARCH-003", "Circular Import Risk", "Package imports a child package that may import back.", "high", regexp.MustCompile(`import\s+\([\s\S]*internal/[\s\S]*internal/`)},
}

func ScanFileForBugs(path string) ([]FindingResult, error) {
	return scanFileWithBugRules(path, bugRules)
}

func ScanFileForSecurity(path string) ([]FindingResult, error) {
	return scanFileWithSecRules(path, secRules)
}

func ScanFileForPerformance(path string) ([]FindingResult, error) {
	return scanFileWithPerfRules(path, perfRules)
}

func ScanFileForLogic(path string) ([]FindingResult, error) {
	return scanFileWithLogicRules(path, logicRules)
}

// ScanFileWithLLM sends file content to an LLM for deep analysis.
func ScanFileWithLLM(path, category, model string) ([]FindingResult, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Limit content size for LLM context
	codeStr := string(content)
	if len(codeStr) > 8000 {
		codeStr = codeStr[:8000]
	}

	config := llm.DefaultConfig()
	if model != "" {
		config.Model = model
	}

	var systemPrompt string
	switch category {
	case "bug":
		systemPrompt = `You are an expert bug detection agent. Analyze the following code for bugs, logic errors, null pointer dereferences, race conditions, off-by-one errors, and type mismatches.
Return your findings as a JSON array with objects containing: title, description, severity (critical/high/medium/low), category ("bug"), line_start, line_end, cwe_id (if applicable), root_cause, impact, confidence (0.0-1.0).
If no bugs are found, return an empty JSON array [].`

	case "security":
		systemPrompt = `You are an expert security vulnerability analyst. Analyze the following code for OWASP Top 10 vulnerabilities, injection attacks, authentication flaws, cryptographic issues, and sensitive data exposure.
Return your findings as a JSON array with objects containing: title, description, severity (critical/high/medium/low), category ("security"), line_start, line_end, cwe_id, cvss_score (0.0-10.0), root_cause, impact, confidence (0.0-1.0).
If no vulnerabilities are found, return an empty JSON array [].`

	case "performance":
		systemPrompt = `You are an expert performance optimization analyst. Analyze the following code for N+1 queries, unnecessary allocations, blocking I/O, inefficient algorithms, memory leaks, and unoptimized loops.
Return your findings as a JSON array with objects containing: title, description, severity (critical/high/medium/low), category ("performance"), line_start, line_end, root_cause, impact, confidence (0.0-1.0).
If no performance issues are found, return an empty JSON array [].`

	case "architecture":
		systemPrompt = `You are an expert software architect. Analyze the following code for anti-patterns including circular dependencies, God objects, tight coupling, SRP violations, inappropriate abstraction levels, and design pattern misuse.
Return your findings as a JSON array with objects containing: title, description, severity (critical/high/medium/low), category ("architecture"), line_start, line_end, root_cause, impact, confidence (0.0-1.0).
If no architecture issues are found, return an empty JSON array [].`

	default:
		return nil, fmt.Errorf("unknown analysis category: %s", category)
	}

	prompt := fmt.Sprintf("Analyze this code:\n\n```\n%s\n```", codeStr)

	response, err := llm.AnalyzeCode(config, systemPrompt, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM analysis failed: %w", err)
	}

	llmFindings, err := llm.ParseFindingsJSON(response)
	if err != nil {
		log.Printf("Failed to parse LLM findings for %s: %v", path, err)
		return nil, nil // Non-fatal
	}

	var results []FindingResult
	for _, f := range llmFindings {
		results = append(results, FindingResult{
			RuleID:      fmt.Sprintf("LLM-%s-%d", strings.ToUpper(category)[:3], f.LineStart),
			Severity:    f.Severity,
			Title:       f.Title,
			Description: f.Description,
			LineStart:   f.LineStart,
			LineEnd:     f.LineEnd,
			Category:    category,
			CweID:       f.CweID,
			CvssScore:   f.CvssScore,
			RootCause:   f.RootCause,
			Impact:      f.Impact,
			Confidence:  f.Confidence,
			Source:      "llm",
		})
	}

	return results, nil
}

func scanFileWithBugRules(path string, rules []struct {
	RuleID      string
	Title       string
	Description string
	Severity    string
	Pattern     *regexp.Regexp
}) ([]FindingResult, error) {
	var findings []FindingResult

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 1
	for scanner.Scan() {
		lineStr := scanner.Text()
		for _, r := range rules {
			if r.Pattern.MatchString(lineStr) {
				findings = append(findings, FindingResult{
					RuleID:      r.RuleID,
					Title:       r.Title,
					Description: r.Description,
					Severity:    r.Severity,
					LineStart:   lineNum,
					LineEnd:     lineNum,
					Category:    "bug",
					Source:      "regex",
				})
			}
		}
		lineNum++
	}

	return findings, nil
}

func scanFileWithSecRules(path string, rules []struct {
	RuleID      string
	Title       string
	Description string
	Severity    string
	CweID       string
	Pattern     *regexp.Regexp
}) ([]FindingResult, error) {
	var findings []FindingResult

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 1
	for scanner.Scan() {
		lineStr := scanner.Text()
		for _, r := range rules {
			if r.Pattern.MatchString(lineStr) {
				findings = append(findings, FindingResult{
					RuleID:      r.RuleID,
					Title:       r.Title,
					Description: r.Description,
					Severity:    r.Severity,
					LineStart:   lineNum,
					LineEnd:     lineNum,
					Category:    "security",
					CweID:       r.CweID,
					Source:      "regex",
				})
			}
		}
		lineNum++
	}

	return findings, nil
}

func scanFileWithPerfRules(path string, rules []struct {
	RuleID      string
	Title       string
	Description string
	Severity    string
	Pattern     *regexp.Regexp
}) ([]FindingResult, error) {
	var findings []FindingResult

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 1
	for scanner.Scan() {
		lineStr := scanner.Text()
		for _, r := range rules {
			if r.Pattern.MatchString(lineStr) {
				findings = append(findings, FindingResult{
					RuleID:      r.RuleID,
					Title:       r.Title,
					Description: r.Description,
					Severity:    r.Severity,
					LineStart:   lineNum,
					LineEnd:     lineNum,
					Category:    "performance",
					Source:      "regex",
				})
			}
		}
		lineNum++
	}

	return findings, nil
}

func scanFileWithLogicRules(path string, rules []struct {
	RuleID      string
	Title       string
	Description string
	Severity    string
	Pattern     *regexp.Regexp
}) ([]FindingResult, error) {
	var findings []FindingResult

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 1
	for scanner.Scan() {
		lineStr := scanner.Text()
		for _, r := range rules {
			if r.Pattern.MatchString(lineStr) {
				findings = append(findings, FindingResult{
					RuleID:      r.RuleID,
					Title:       r.Title,
					Description: r.Description,
					Severity:    r.Severity,
					LineStart:   lineNum,
					LineEnd:     lineNum,
					Category:    "architecture",
					Source:      "regex",
				})
			}
		}
		lineNum++
	}

	return findings, nil
}
