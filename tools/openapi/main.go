package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var routePattern = regexp.MustCompile(`"(GET|POST|PUT|DELETE) (/[^" ]+)"`)
var parameterPattern = regexp.MustCompile(`\{([^}]+)\}`)

type operation struct {
	Tags        []string         `json:"tags"`
	OperationID string           `json:"operationId"`
	Summary     string           `json:"summary"`
	Parameters  []map[string]any `json:"parameters,omitempty"`
	RequestBody map[string]any   `json:"requestBody,omitempty"`
	Responses   map[string]any   `json:"responses"`
	Security    []map[string]any `json:"security,omitempty"`
}

type document struct {
	OpenAPI    string                          `json:"openapi"`
	Info       map[string]string               `json:"info"`
	Servers    []map[string]string             `json:"servers"`
	Paths      map[string]map[string]operation `json:"paths"`
	Components map[string]any                  `json:"components"`
}

func main() {
	check := flag.Bool("check", false, "verify generated document")
	output := flag.String("output", "docs/openapi.json", "document path")
	flag.Parse()
	data, err := generate(flag.Args())
	if err != nil {
		fatal(err)
	}
	if *check {
		current, err := os.ReadFile(*output)
		if err != nil {
			fatal(err)
		}
		if !bytes.Equal(bytes.TrimSpace(current), bytes.TrimSpace(data)) {
			fatal(fmt.Errorf("%s is stale; run make openapi", *output))
		}
		return
	}
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		fatal(err)
	}
}

func generate(patterns []string) ([]byte, error) {
	if len(patterns) == 0 {
		patterns = []string{"apps/server/internal/api/routes_*.go"}
	}
	routes := map[string]map[string]operation{}
	for _, pattern := range patterns {
		files, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			content, err := os.ReadFile(file)
			if err != nil {
				return nil, err
			}
			for _, match := range routePattern.FindAllStringSubmatch(string(content), -1) {
				method, path := strings.ToLower(match[1]), match[2]
				if routes[path] == nil {
					routes[path] = map[string]operation{}
				}
				if _, exists := routes[path][method]; exists {
					return nil, fmt.Errorf("duplicate route %s %s", method, path)
				}
				routes[path][method] = buildOperation(method, path)
			}
		}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("no routes found")
	}
	doc := document{
		OpenAPI: "3.1.0",
		Info: map[string]string{
			"title":       "KloudView API",
			"version":     "0.1.0",
			"description": "Infrastructure inventory, observation, incident response, execution, and access control API.",
		},
		Servers: []map[string]string{{"url": "/", "description": "Same-origin Server"}},
		Paths:   routes,
		Components: map[string]any{
			"securitySchemes": map[string]any{
				"agentBearer": map[string]string{"type": "http", "scheme": "bearer"},
				"headerSubject": map[string]string{"type": "apiKey", "in": "header", "name": "X-KloudView-Subject"},
			},
			"schemas": map[string]any{
				"Error": map[string]any{"type": "object", "properties": map[string]any{"error": map[string]any{"type": "object"}}},
			},
		},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	return append(data, '\n'), err
}

func buildOperation(method, path string) operation {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	parts := []string{method}
	for _, segment := range segments {
		segment = strings.Trim(segment, "{}")
		segment = strings.ReplaceAll(segment, "-", " ")
		for _, word := range strings.Fields(segment) {
			parts = append(parts, strings.ToUpper(word[:1])+word[1:])
		}
	}
	tag := "system"
	if len(segments) > 2 && segments[0] == "api" {
		tag = strings.Trim(segments[2], "{}")
	}
	op := operation{
		Tags:        []string{tag},
		OperationID: strings.Join(parts, ""),
		Summary:     strings.ToUpper(method) + " " + path,
		Responses: map[string]any{
			"2XX":     map[string]string{"description": "Successful response"},
			"default": map[string]any{"description": "Error response", "content": map[string]any{"application/json": map[string]any{"schema": map[string]string{"$ref": "#/components/schemas/Error"}}}},
		},
	}
	for _, match := range parameterPattern.FindAllStringSubmatch(path, -1) {
		op.Parameters = append(op.Parameters, map[string]any{"name": match[1], "in": "path", "required": true, "schema": map[string]string{"type": "string"}})
	}
	if method == "post" || method == "put" {
		op.RequestBody = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]string{"type": "object"}}}}
	}
	agentRoute := strings.Contains(path, "/agents/{id}/") && method == "post" ||
		strings.Contains(path, "/agents/{id}/") && method == "put"
	if agentRoute {
		op.Security = []map[string]any{{"agentBearer": []string{}}}
	} else if path != "/healthz" && path != "/api/v1/agents/enroll" {
		op.Security = []map[string]any{{"headerSubject": []string{}}}
	}
	return op
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
