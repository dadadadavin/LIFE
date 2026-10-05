package main

import (
	"strings"
	"testing"
)

func TestMathEval(t *testing.T) {
	tests := []struct {
		expr     string
		expected float64
	}{
		{"2 + 2", 4},
		{"10 - 3 * 2", 4},
		{"(10 - 3) * 2", 14},
		{"sqrt(256) * 12 + 44", 236},
		{"2^10", 1024},
		{"2**10", 1024},
		{"sin(0)", 0},
		{"cos(0)", 1},
		{"abs(-42)", 42},
		{"pow(2, 8)", 256},
		{"min(10, 20)", 10},
		{"max(10, 20)", 20},
		{"pi * 2", 6.283185307179586},
	}

	for _, tt := range tests {
		val, err := evalMathExpression(tt.expr)
		if err != nil {
			t.Errorf("evalMathExpression(%q) failed: %v", tt.expr, err)
			continue
		}
		if val != tt.expected && (val-tt.expected > 1e-9 || tt.expected-val > 1e-9) {
			t.Errorf("evalMathExpression(%q) = %v, want %v", tt.expr, val, tt.expected)
		}
	}
}

func TestToolsExecution(t *testing.T) {
	initPaths()

	// 1. get_mac_system_info
	res := executeLocalTool("get_mac_system_info", nil)
	if res["current_time"] == nil || res["os"] == nil {
		t.Errorf("get_mac_system_info returned incomplete info: %v", res)
	}

	// 2. list_project_files
	res = executeLocalTool("list_project_files", map[string]any{"subdir": ""})
	if res["files"] == nil {
		t.Errorf("list_project_files failed: %v", res)
	}

	// 3. calculate_math
	res = executeLocalTool("calculate_math", map[string]any{"expression": "100 / 4 + 75"})
	if res["result"] != 100.0 {
		t.Errorf("calculate_math wrong result: %v", res)
	}

	// 4. search_user_memory
	res = executeLocalTool("search_user_memory", map[string]any{"query": "macOS"})
	if res["count"] == nil {
		t.Errorf("search_user_memory failed: %v", res)
	}

	// 5. read_project_file
	res = executeLocalTool("read_project_file", map[string]any{"filename": "README.md"})
	if res["content"] == nil && res["error"] == nil {
		t.Errorf("read_project_file failed: %v", res)
	}

	// 6. Security check: cannot read .env
	res = executeLocalTool("read_project_file", map[string]any{"filename": ".env"})
	if res["error"] == nil {
		t.Errorf("read_project_file allowed reading .env!")
	}

	// 7. Security check: path traversal
	res = executeLocalTool("read_project_file", map[string]any{"filename": "../../etc/passwd"})
	if res["error"] == nil {
		t.Errorf("read_project_file allowed path traversal!")
	}

	// 8. web_search
	res = executeLocalTool("web_search", map[string]any{"query": "Google Gemini"})
	if res["results"] == nil {
		t.Errorf("web_search returned nil results: %v", res)
	}
}

func TestCleanHTMLText(t *testing.T) {
	raw := `<header><nav><a href="/">Home</a></nav></header><script>console.log("secret code");</script><style>body { color: red; }</style><p>Hello, <strong>Gemini 3.8</strong> &amp; World!</p>`
	clean := cleanHTMLText(raw)
	if strings.Contains(clean, "secret code") {
		t.Errorf("cleanHTMLText did not strip script content: %q", clean)
	}
	if strings.Contains(clean, "color: red") {
		t.Errorf("cleanHTMLText did not strip style content: %q", clean)
	}
	if !strings.Contains(clean, "Hello, Gemini 3.8 & World!") {
		t.Errorf("cleanHTMLText did not preserve text properly: %q", clean)
	}
}

