package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"google.golang.org/genai"
)

type ToolSpec struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Category    string         `json:"category"`
	SampleArgs  string         `json:"sample_args"`
	Schema      *genai.Schema  `json:"-"`
}

var toolCatalog = []ToolSpec{
	{
		Name:        "get_mac_system_info",
		Title:       "Mac System & Battery Status",
		Description: "Get real-time Mac battery percentage, power source, CPU cores, load average, free disk space, macOS version, and current local time.",
		Category:    "system",
		SampleArgs:  `{}`,
		Schema: &genai.Schema{
			Type:       genai.TypeObject,
			Properties: map[string]*genai.Schema{},
		},
	},
	{
		Name:        "web_search",
		Title:       "Deep Multi-Source Web Search",
		Description: "Search the live web (DuckDuckGo Web results, Google News RSS, and English + Indonesian Wikipedia) for current events, programs, companies, or technical facts.",
		Category:    "web",
		SampleArgs:  `{"query": "Samsung Innovation Campus Indonesia"}`,
		Schema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"query": {
					Type:        genai.TypeString,
					Description: "The search query to look up on the web.",
				},
			},
			Required: []string{"query"},
		},
	},
	{
		Name:        "read_webpage",
		Title:       "Read Full Webpage Content",
		Description: "Fetch and extract clean readable article text from any URL (e.g. from a web_search result) to research a topic in depth.",
		Category:    "web",
		SampleArgs:  `{"url": "https://en.wikipedia.org/wiki/Samsung_Electronics"}`,
		Schema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"url": {
					Type:        genai.TypeString,
					Description: "Full http:// or https:// URL to fetch and read.",
				},
			},
			Required: []string{"url"},
		},
	},
	{
		Name:        "crawl_website",
		Title:       "Tavily Website Deep Crawl",
		Description: "Systematically crawl a website domain or documentation using Tavily to explore multiple subpages and extract structured knowledge.",
		Category:    "web",
		SampleArgs:  `{"url": "https://docs.tavily.com", "instructions": "Find Python SDK endpoints"}`,
		Schema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"url": {
					Type:        genai.TypeString,
					Description: "Target website or documentation URL to crawl.",
				},
				"instructions": {
					Type:        genai.TypeString,
					Description: "Optional focus instructions (e.g. 'Extract all pricing and API details').",
				},
			},
			Required: []string{"url"},
		},
	},
	{
		Name:        "memory",
		Title:       "Unified Long-Term Memory & Local RAG",
		Description: "Deep semantic search and stateful management of episodic, technical, and preference memories. Use 'recall' to retrieve past discussions and code context; use 'remember' to record new detailed knowledge; use 'update' to amend existing knowledge; use 'forget' to remove obsolete knowledge.",
		Category:    "memory",
		SampleArgs:  `{"action": "recall", "query": "macOS audio setup and project architecture"}`,
		Schema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"action": {
					Type:        genai.TypeString,
					Description: "Action to perform: 'recall' (semantic search), 'remember' (save new knowledge), 'update' (amend existing memory), or 'forget' (delete memory).",
					Enum:        []string{"recall", "remember", "update", "forget"},
				},
				"query": {
					Type:        genai.TypeString,
					Description: "Search query for semantic retrieval (used with 'recall').",
				},
				"content": {
					Type:        genai.TypeString,
					Description: "Rich multi-paragraph factual detail to store or update (used with 'remember' and 'update').",
				},
				"id": {
					Type:        genai.TypeString,
					Description: "Memory chunk UUID (optional, for 'update' or 'forget').",
				},
			},
			Required: []string{"action"},
		},
	},
	{
		Name:        "list_project_files",
		Title:       "List Workspace Files",
		Description: "List files and directories inside the user's local project workspace.",
		Category:    "files",
		SampleArgs:  `{"subdir": ""}`,
		Schema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"subdir": {
					Type:        genai.TypeString,
					Description: "Optional relative subdirectory path (default is root workspace).",
				},
			},
		},
	},
	{
		Name:        "read_project_file",
		Title:       "Read Workspace File",
		Description: "Read the text contents of a file inside the project workspace.",
		Category:    "files",
		SampleArgs:  `{"filename": "README.md"}`,
		Schema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"filename": {
					Type:        genai.TypeString,
					Description: "Relative path to the file in the workspace (e.g. 'README.md').",
				},
			},
			Required: []string{"filename"},
		},
	},
	{
		Name:        "write_project_note",
		Title:       "Save Markdown Note",
		Description: "Create or append a markdown note, code snippet, or action item inside the workspace notes/ folder.",
		Category:    "files",
		SampleArgs:  `{"title": "ideas", "content": "Test Go + HTMX WebSocket bridge"}`,
		Schema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"title": {
					Type:        genai.TypeString,
					Description: "Short filename/title for the note (e.g. 'ideas' or 'todo').",
				},
				"content": {
					Type:        genai.TypeString,
					Description: "Markdown content to write to the note.",
				},
			},
			Required: []string{"title", "content"},
		},
	},
	{
		Name:        "calculate_math",
		Title:       "Math & Expression Calculator",
		Description: "Evaluate a mathematical expression accurately (supports arithmetic, sqrt, sin, cos, log, pi, powers).",
		Category:    "utility",
		SampleArgs:  `{"expression": "sqrt(256) * 12 + 44"}`,
		Schema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"expression": {
					Type:        genai.TypeString,
					Description: "Mathematical expression to evaluate, e.g. 'sqrt(144) * 25'.",
				},
			},
			Required: []string{"expression"},
		},
	},
	{
		Name:        "mac_clipboard",
		Title:       "Mac Clipboard (Read / Copy)",
		Description: "Read the user's current macOS clipboard text (pbpaste) or copy text/links/code directly to the user's clipboard (pbcopy).",
		Category:    "mac",
		SampleArgs:  `{"action": "read"}`,
		Schema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"action": {
					Type:        genai.TypeString,
					Description: "'read' to read current clipboard, or 'write' to copy text to clipboard.",
				},
				"text": {
					Type:        genai.TypeString,
					Description: "Text to copy to clipboard when action is 'write'.",
				},
			},
			Required: []string{"action"},
		},
	},
	{
		Name:        "open_url_or_app",
		Title:       "Open URL or Workspace File on Mac",
		Description: "Open an http/https URL in the user's Mac browser or reveal a workspace file/folder in Finder when requested.",
		Category:    "mac",
		SampleArgs:  `{"target": "https://aistudio.google.com"}`,
		Schema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"target": {
					Type:        genai.TypeString,
					Description: "Full https:// URL to open in browser, or relative workspace path to open.",
				},
			},
			Required: []string{"target"},
		},
	},
}

func buildGenAITools(cfg AppConfig) []*genai.Tool {
	enabled := make(map[string]bool)
	for _, name := range cfg.EnabledTools {
		enabled[name] = true
	}

	behavior := genai.BehaviorNonBlocking
	if cfg.ToolBehavior == "BLOCKING" {
		behavior = genai.BehaviorBlocking
	}

	var decls []*genai.FunctionDeclaration
	for _, spec := range toolCatalog {
		if enabled[spec.Name] {
			decls = append(decls, &genai.FunctionDeclaration{
				Name:        spec.Name,
				Description: spec.Description,
				Parameters:  spec.Schema,
				Behavior:    behavior,
			})
		}
	}
	if len(decls) == 0 {
		return nil
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

var (
	reHTMLTags   = regexp.MustCompile(`<[^>]+>`)
	reScriptTags = regexp.MustCompile(`(?is)<(script|style|noscript|svg|header|footer|nav)[^>]*>.*?</(script|style|noscript|svg|header|footer|nav)>`)
	reWhitespace = regexp.MustCompile(`\s+`)
	reSafeTitle  = regexp.MustCompile(`[^a-zA-Z0-9_\-]`)
)

func cleanHTMLText(raw string) string {
	s := reScriptTags.ReplaceAllString(raw, " ")
	s = reHTMLTags.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = reWhitespace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// truncateToolOutput recursively guards string values in tool execution outputs,
// capping strings to maxChars (default 8,000 chars ~ 2,000 tokens) to prevent context blowout
// in Gemini Live.
func truncateToolOutput(m map[string]any, maxChars int) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = truncateToolValue(v, maxChars)
	}
	return out
}

func truncateToolValue(v any, maxChars int) any {
	switch val := v.(type) {
	case string:
		if len(val) > maxChars {
			return val[:maxChars] + fmt.Sprintf("\n... [Output truncated from %d to %d characters to preserve Gemini Live context window]", len(val), maxChars)
		}
		return val
	case map[string]any:
		return truncateToolOutput(val, maxChars)
	case []any:
		res := make([]any, len(val))
		for i, elem := range val {
			res[i] = truncateToolValue(elem, maxChars)
		}
		return res
	case []map[string]any:
		res := make([]map[string]any, len(val))
		for i, elem := range val {
			res[i] = truncateToolOutput(elem, maxChars)
		}
		return res
	default:
		return v
	}
}

func executeLocalTool(name string, args map[string]any) map[string]any {
	raw := runLocalTool(name, args)
	return truncateToolOutput(raw, 8000)
}

func runLocalTool(name string, args map[string]any) map[string]any {
	strArg := func(k string) string {
		if v, ok := args[k]; ok && v != nil {
			return strings.TrimSpace(fmt.Sprintf("%v", v))
		}
		return ""
	}

	switch name {
	case "get_mac_system_info":
		battInfo := "Unknown"
		if out, err := exec.Command("pmset", "-g", "batt").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			for i := range lines {
				lines[i] = strings.TrimSpace(lines[i])
			}
			battInfo = strings.Join(lines, " | ")
		}

		macVer := ""
		if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
			macVer = strings.TrimSpace(string(out))
		}

		loadAvg := ""
		if out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
			loadAvg = strings.Trim(strings.TrimSpace(string(out)), "{}")
		}

		var stat syscall.Statfs_t
		freeGB := 0.0
		totalGB := 0.0
		if syscall.Statfs(baseDir, &stat) == nil {
			freeGB = float64(stat.Bavail*uint64(stat.Bsize)) / (1024 * 1024 * 1024)
			totalGB = float64(stat.Blocks*uint64(stat.Bsize)) / (1024 * 1024 * 1024)
		}

		return map[string]any{
			"current_time":  time.Now().Format("2006-01-02 15:04:05 (Monday)"),
			"os":            fmt.Sprintf("macOS %s (%s/%s)", macVer, runtime.GOOS, runtime.GOARCH),
			"cpu_cores":     runtime.NumCPU(),
			"cpu_load_avg":  strings.TrimSpace(loadAvg),
			"battery":       battInfo,
			"disk_free_gb":  fmt.Sprintf("%.2f GB free of %.2f GB", freeGB, totalGB),
			"workspace_dir": baseDir,
		}

	case "memory":
		action := strings.ToLower(strArg("action"))
		if action == "" {
			action = "recall"
		}
		query := strArg("query")
		content := strArg("content")
		id := strArg("id")
		isAsync, _ := args["_async"].(bool)
		prefKey := strArg("_preferred_key")

		switch action {
		case "recall":
			scored, err := SearchRAGMemory(query, 3, prefKey)
			if err != nil {
				return map[string]any{"action": "recall", "query": query, "error": err.Error()}
			}
			if len(scored) == 0 {
				return map[string]any{
					"action":  "recall",
					"query":   query,
					"count":   0,
					"message": "No relevant memories found in local RAG store.",
				}
			}
			var md strings.Builder
			md.WriteString(fmt.Sprintf("### Recalled Memories for %q:\n", query))
			var results []map[string]any
			for _, s := range scored {
				c := s.Chunk
				md.WriteString(fmt.Sprintf("\n#### [%s] %s (Score: %.2f | Similarity: %.2f | Recency: %.2f)\n%s\n",
					strings.ToUpper(c.Category), c.Title, s.HybridScore, s.CosineSimilarity, s.RecencyScore, c.Content))
				results = append(results, map[string]any{
					"id":         c.ID,
					"title":      c.Title,
					"category":   c.Category,
					"content":    c.Content,
					"score":      s.HybridScore,
					"similarity": s.CosineSimilarity,
					"recency":    s.RecencyScore,
					"updated_at": c.UpdatedAt.Format(time.RFC3339),
				})
			}
			return map[string]any{
				"action":   "recall",
				"query":    query,
				"count":    len(scored),
				"markdown": md.String(),
				"results":  results,
			}

		case "remember":
			if content == "" {
				return map[string]any{"error": "content is required for remember action"}
			}
			category := strArg("category")
			if category == "" {
				category = "episodic"
			}
			title := strArg("title")
			if title == "" {
				title = "Voice Memory"
			}
			if isAsync {
				go func() {
					_, _ = InsertRAGChunk(title, category, content, prefKey)
				}()
				return map[string]any{
					"status":  "queued",
					"action":  "remember",
					"message": "Memory operation initiated asynchronously in background.",
				}
			}
			ch, err := InsertRAGChunk(title, category, content, prefKey)
			if err != nil {
				return map[string]any{"action": "remember", "error": err.Error()}
			}
			return map[string]any{"status": "saved", "action": "remember", "id": ch.ID, "title": ch.Title}

		case "update":
			if id == "" {
				return map[string]any{"error": "id is required for update action"}
			}
			if isAsync {
				go func() {
					_ = UpdateRAGChunk(id, strArg("title"), strArg("category"), content, prefKey)
				}()
				return map[string]any{
					"status":  "queued",
					"action":  "update",
					"message": "Memory update initiated asynchronously in background.",
				}
			}
			err := UpdateRAGChunk(id, strArg("title"), strArg("category"), content, prefKey)
			if err != nil {
				return map[string]any{"action": "update", "error": err.Error()}
			}
			return map[string]any{"status": "updated", "action": "update", "id": id}

		case "forget":
			if id == "" {
				return map[string]any{"error": "id is required for forget action"}
			}
			if isAsync {
				go func() {
					_ = DeleteRAGChunk(id)
				}()
				return map[string]any{
					"status":  "queued",
					"action":  "forget",
					"message": "Memory deletion initiated asynchronously in background.",
				}
			}
			err := DeleteRAGChunk(id)
			if err != nil {
				return map[string]any{"action": "forget", "error": err.Error()}
			}
			return map[string]any{"status": "deleted", "action": "forget", "id": id}

		default:
			return map[string]any{"error": fmt.Sprintf("unsupported memory action %q", action)}
		}

	// Legacy backward compatibility redirects
	case "save_user_memory":
		content := strArg("content")
		category := strArg("category")
		if category == "" {
			category = "fact"
		}
		if content == "" {
			return map[string]any{"error": "content is required"}
		}
		item := addMemoryItem(content, category, "voice_tool", false)
		_, _ = InsertRAGChunk("User Memory", category, content, "")
		return map[string]any{"status": "saved", "memory": item}

	case "search_user_memory":
		q := strArg("query")
		scored, _ := SearchRAGMemory(q, 10, "")
		var results []any
		for _, s := range scored {
			results = append(results, s.Chunk)
		}
		return map[string]any{"count": len(results), "memories": results}

	case "web_search":
		query := strArg("query")
		if query == "" {
			return map[string]any{"error": "query is required"}
		}
		cfg := loadConfig()
		apiKey := getTavilyAPIKey()
		if cfg.WebSearchProvider == "duckduckgo" || apiKey == "" {
			return performDeepWebSearch(query)
		}
		depth := cfg.TavilySearchDepth
		if depth == "" {
			depth = "advanced"
		}
		tavilyRes, err := searchTavily(apiKey, query, depth)
		if err != nil {
			fmt.Printf("%s \033[33m[TAVILY WARNING]\033[0m Search error (%v), falling back to multi-source...\n", time.Now().Format("15:04:05"), err)
			return performDeepWebSearch(query)
		}
		if cfg.WebSearchProvider == "hybrid" {
			localRes := performDeepWebSearch(query)
			if items, ok := localRes["results"].([]map[string]any); ok {
				if tItems, ok := tavilyRes["results"].([]map[string]any); ok {
					tavilyRes["results"] = append(tItems, items...)
				}
			}
		}
		return tavilyRes

	case "read_webpage":
		rawURL := strArg("url")
		if rawURL == "" || (!strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://")) {
			return map[string]any{"error": "valid http:// or https:// url is required"}
		}
		if apiKey := getTavilyAPIKey(); apiKey != "" {
			if res, err := extractTavily(apiKey, []string{rawURL}); err == nil && len(res) > 0 {
				return res
			}
		}
		return fetchReadableWebpage(rawURL)

	case "crawl_website":
		rawURL := strArg("url")
		if rawURL == "" || (!strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://")) {
			return map[string]any{"error": "valid http:// or https:// url is required"}
		}
		apiKey := getTavilyAPIKey()
		if apiKey == "" {
			return map[string]any{"error": "Tavily API key is not configured. Set TAVILY_API_KEY in .env"}
		}
		instructions := strArg("instructions")
		res, err := crawlTavily(apiKey, rawURL, instructions)
		if err != nil {
			return map[string]any{"error": fmt.Sprintf("crawl failed: %v", err)}
		}
		return res

	case "list_project_files":
		subdir := strings.TrimLeft(strArg("subdir"), "/")
		target := filepath.Clean(filepath.Join(baseDir, subdir))
		if !strings.HasPrefix(target, baseDir) {
			return map[string]any{"error": "access outside workspace is restricted"}
		}
		entries, err := os.ReadDir(target)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		var files []map[string]any
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") && name != ".env.example" && name != ".gitignore" {
				continue
			}
			info, _ := e.Info()
			item := map[string]any{
				"name": name,
				"type": "file",
			}
			if e.IsDir() {
				item["type"] = "dir"
			} else if info != nil {
				item["size_bytes"] = info.Size()
			}
			files = append(files, item)
		}
		sort.Slice(files, func(i, j int) bool {
			return fmt.Sprintf("%v", files[i]["name"]) < fmt.Sprintf("%v", files[j]["name"])
		})
		rel, _ := filepath.Rel(baseDir, target)
		return map[string]any{"directory": rel, "files": files}

	case "read_project_file":
		filename := strings.TrimLeft(strArg("filename"), "/")
		if filename == ".env" {
			return map[string]any{"error": "reading .env directly via tool is restricted"}
		}
		target := filepath.Clean(filepath.Join(baseDir, filename))
		if !strings.HasPrefix(target, baseDir) {
			return map[string]any{"error": "access outside workspace is restricted"}
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		content := string(data)
		totalChars := len(content)
		if len(content) > 5000 {
			content = content[:5000] + "\n...[truncated]"
		}
		return map[string]any{
			"filename":    filename,
			"total_chars": totalChars,
			"content":     content,
		}

	case "write_project_note":
		title := reSafeTitle.ReplaceAllString(strArg("title"), "_")
		if title == "" {
			title = "note"
		}
		if !strings.HasSuffix(title, ".md") {
			title += ".md"
		}
		content := strArg("content")
		notePath := filepath.Join(notesDir, title)
		ts := time.Now().Format("2006-01-02 15:04:05")
		entry := fmt.Sprintf("\n## Note (%s)\n\n%s\n", ts, content)
		if existing, err := os.ReadFile(notePath); err == nil {
			_ = os.WriteFile(notePath, append(existing, []byte(entry)...), 0644)
		} else {
			header := fmt.Sprintf("# %s\n%s", strings.TrimSuffix(title, ".md"), entry)
			_ = os.WriteFile(notePath, []byte(header), 0644)
		}
		return map[string]any{"status": "saved", "file": "notes/" + title}

	case "calculate_math":
		expr := strArg("expression")
		if expr == "" {
			return map[string]any{"error": "expression is required"}
		}
		val, err := evalMathExpression(expr)
		if err != nil {
			return map[string]any{"expression": expr, "error": err.Error()}
		}
		formatted := strconv.FormatFloat(val, 'f', -1, 64)
		return map[string]any{
			"expression": expr,
			"result":     val,
			"formatted":  formatted,
		}

	case "mac_clipboard":
		action := strings.ToLower(strArg("action"))
		if action == "write" || action == "copy" {
			txt := strArg("text")
			cmd := exec.Command("pbcopy")
			cmd.Stdin = strings.NewReader(txt)
			if err := cmd.Run(); err != nil {
				return map[string]any{"error": err.Error()}
			}
			return map[string]any{"status": "copied_to_clipboard", "chars": len(txt)}
		}
		out, err := exec.Command("pbpaste").Output()
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		clip := string(out)
		if len(clip) > 3000 {
			clip = clip[:3000] + "\n...[truncated]"
		}
		return map[string]any{"status": "ok", "clipboard": clip}

	case "open_url_or_app":
		target := strArg("target")
		if target == "" {
			return map[string]any{"error": "target is required"}
		}
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			if err := exec.Command("open", target).Start(); err != nil {
				return map[string]any{"error": err.Error()}
			}
			return map[string]any{"status": "opened_url", "target": target}
		}
		// Check if target is an existing file inside workspace first
		fullPath := filepath.Clean(filepath.Join(baseDir, target))
		if strings.HasPrefix(fullPath, baseDir) {
			if _, err := os.Stat(fullPath); err == nil {
				if err := exec.Command("open", fullPath).Start(); err != nil {
					return map[string]any{"error": err.Error()}
				}
				return map[string]any{"status": "opened_workspace_path", "target": target}
			}
		}
		// Otherwise treat target as a macOS application name (e.g., "Calculator", "Safari", "Notes")
		if !strings.Contains(target, "/") && !strings.Contains(target, "..") {
			if err := exec.Command("open", "-a", target).Start(); err != nil {
				return map[string]any{"error": fmt.Sprintf("could not open application %q: %v", target, err)}
			}
			return map[string]any{"status": "opened_application", "app": target}
		}
		return map[string]any{"error": "target not found in workspace and is not a valid application name"}
	}

	return map[string]any{"error": fmt.Sprintf("unknown tool %q", name)}
}

// ==============================================================================
// Native Go Recursive-Descent Mathematical Expression Evaluator
// ==============================================================================

type mathParser struct {
	input string
	pos   int
}

func evalMathExpression(expr string) (float64, error) {
	cleaned := strings.ReplaceAll(expr, "**", "^")
	p := &mathParser{input: cleaned}
	val, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipSpaces()
	if p.pos < len(p.input) {
		return 0, fmt.Errorf("unexpected token at %q", p.input[p.pos:])
	}
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return 0, fmt.Errorf("math result is undefined or infinite")
	}
	return val, nil
}

func (p *mathParser) skipSpaces() {
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

func (p *mathParser) parseExpr() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) {
			break
		}
		op := p.input[p.pos]
		if op != '+' && op != '-' {
			break
		}
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
	return left, nil
}

func (p *mathParser) parseTerm() (float64, error) {
	left, err := p.parsePower()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) {
			break
		}
		op := p.input[p.pos]
		if op != '*' && op != '/' && op != '%' {
			break
		}
		p.pos++
		right, err := p.parsePower()
		if err != nil {
			return 0, err
		}
		switch op {
		case '*':
			left *= right
		case '/':
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		case '%':
			if right == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			left = math.Mod(left, right)
		}
	}
	return left, nil
}

func (p *mathParser) parsePower() (float64, error) {
	base, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	p.skipSpaces()
	if p.pos < len(p.input) && p.input[p.pos] == '^' {
		p.pos++
		exp, err := p.parsePower()
		if err != nil {
			return 0, err
		}
		return math.Pow(base, exp), nil
	}
	return base, nil
}

func (p *mathParser) parseUnary() (float64, error) {
	p.skipSpaces()
	if p.pos < len(p.input) {
		if p.input[p.pos] == '+' {
			p.pos++
			return p.parseUnary()
		}
		if p.input[p.pos] == '-' {
			p.pos++
			v, err := p.parseUnary()
			return -v, err
		}
	}
	return p.parsePrimary()
}

func (p *mathParser) parsePrimary() (float64, error) {
	p.skipSpaces()
	if p.pos >= len(p.input) {
		return 0, fmt.Errorf("unexpected end of expression")
	}
	if p.input[p.pos] == '(' {
		p.pos++
		val, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpaces()
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return val, nil
	}

	// Identifier (constant or function call)
	if unicode.IsLetter(rune(p.input[p.pos])) {
		start := p.pos
		for p.pos < len(p.input) && (unicode.IsLetter(rune(p.input[p.pos])) || unicode.IsDigit(rune(p.input[p.pos]))) {
			p.pos++
		}
		ident := strings.ToLower(p.input[start:p.pos])
		p.skipSpaces()
		if p.pos < len(p.input) && p.input[p.pos] == '(' {
			p.pos++
			arg1, err := p.parseExpr()
			if err != nil {
				return 0, err
			}
			var arg2 float64
			hasArg2 := false
			p.skipSpaces()
			if p.pos < len(p.input) && p.input[p.pos] == ',' {
				p.pos++
				arg2, err = p.parseExpr()
				if err != nil {
					return 0, err
				}
				hasArg2 = true
			}
			p.skipSpaces()
			if p.pos >= len(p.input) || p.input[p.pos] != ')' {
				return 0, fmt.Errorf("missing ')' after function %s", ident)
			}
			p.pos++
			switch ident {
			case "sqrt":
				return math.Sqrt(arg1), nil
			case "sin":
				return math.Sin(arg1), nil
			case "cos":
				return math.Cos(arg1), nil
			case "tan":
				return math.Tan(arg1), nil
			case "asin":
				return math.Asin(arg1), nil
			case "acos":
				return math.Acos(arg1), nil
			case "atan":
				return math.Atan(arg1), nil
			case "log", "ln":
				return math.Log(arg1), nil
			case "log10":
				return math.Log10(arg1), nil
			case "log2":
				return math.Log2(arg1), nil
			case "exp":
				return math.Exp(arg1), nil
			case "abs":
				return math.Abs(arg1), nil
			case "floor":
				return math.Floor(arg1), nil
			case "ceil":
				return math.Ceil(arg1), nil
			case "round":
				return math.Round(arg1), nil
			case "pow":
				if !hasArg2 {
					return 0, fmt.Errorf("pow(x, y) requires 2 arguments")
				}
				return math.Pow(arg1, arg2), nil
			case "min":
				if !hasArg2 {
					return arg1, nil
				}
				return math.Min(arg1, arg2), nil
			case "max":
				if !hasArg2 {
					return arg1, nil
				}
				return math.Max(arg1, arg2), nil
			default:
				return 0, fmt.Errorf("unknown math function %q", ident)
			}
		}
		switch ident {
		case "pi":
			return math.Pi, nil
		case "e":
			return math.E, nil
		default:
			return 0, fmt.Errorf("unknown constant %q", ident)
		}
	}

	// Number literal
	start := p.pos
	for p.pos < len(p.input) && (unicode.IsDigit(rune(p.input[p.pos])) || p.input[p.pos] == '.') {
		p.pos++
	}
	if p.pos < len(p.input) && (p.input[p.pos] == 'e' || p.input[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.input) && (p.input[p.pos] == '+' || p.input[p.pos] == '-') {
			p.pos++
		}
		for p.pos < len(p.input) && unicode.IsDigit(rune(p.input[p.pos])) {
			p.pos++
		}
	}
	if start == p.pos {
		return 0, fmt.Errorf("expected number at %q", p.input[p.pos:])
	}
	return strconv.ParseFloat(p.input[start:p.pos], 64)
}

// ==============================================================================
// Deep Multi-Source Web Search (DuckDuckGo HTML + Google News RSS + Wiki EN/ID)
// ==============================================================================

func performDeepWebSearch(query string) map[string]any {
	client := &http.Client{Timeout: 5 * time.Second}
	var results []map[string]any

	// 1. DuckDuckGo Organic Web Search (HTML endpoint)
	ddgResults := searchDuckDuckGoHTML(client, query)
	results = append(results, ddgResults...)

	// 2. Google News RSS Search (great for local/current programs like Samsung Innovation Campus Indonesia)
	newsResults := searchGoogleNewsRSS(client, query)
	results = append(results, newsResults...)

	// 3. Wikipedia (English & Indonesian)
	wikiEN := searchWikipedia(client, "en", query)
	results = append(results, wikiEN...)
	wikiID := searchWikipedia(client, "id", query)
	results = append(results, wikiID...)

	if len(results) > 10 {
		results = results[:10]
	}

	if len(results) == 0 {
		results = append(results, map[string]any{
			"note": fmt.Sprintf("No direct results found for %q.", query),
		})
	}

	return map[string]any{
		"query":   query,
		"count":   len(results),
		"results": results,
		"tip":     "Use read_webpage with any result's 'url' if you need to read the full article details.",
	}
}

var (
	reDDGResultBlock = regexp.MustCompile(`(?is)<a[^>]+class="[^"]*result__a[^"]*"[^>]+href="([^"]+)"[^>]*>(.*?)</a>.*?<a[^>]+class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
)

func searchDuckDuckGoHTML(client *http.Client, query string) []map[string]any {
	form := url.Values{}
	form.Set("q", query)
	req, err := http.NewRequest("POST", "https://html.duckduckgo.com/html/", strings.NewReader(form.Encode()))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 250000))
	if err != nil {
		return nil
	}
	page := string(bodyBytes)

	matches := reDDGResultBlock.FindAllStringSubmatch(page, 5)
	var out []map[string]any
	for _, m := range matches {
		rawHref := html.UnescapeString(m[1])
		if parsed, err := url.Parse(rawHref); err == nil {
			if uddg := parsed.Query().Get("uddg"); uddg != "" {
				rawHref = uddg
			}
		}
		title := cleanHTMLText(m[2])
		snippet := cleanHTMLText(m[3])
		if title != "" && snippet != "" {
			out = append(out, map[string]any{
				"source":  "Web",
				"title":   title,
				"snippet": snippet,
				"url":     rawHref,
			})
		}
	}
	return out
}

type rssFeed struct {
	Items []struct {
		Title   string `xml:"title"`
		Link    string `xml:"link"`
		PubDate string `xml:"pubDate"`
		Source  string `xml:"source"`
	} `xml:"channel>item"`
}

func searchGoogleNewsRSS(client *http.Client, query string) []map[string]any {
	u := fmt.Sprintf("https://news.google.com/rss/search?q=%s&hl=en-ID&gl=ID&ceid=ID:en", url.QueryEscape(query))
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "LifelGoBot/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 150000))
	if err != nil {
		return nil
	}
	var feed rssFeed
	if xml.Unmarshal(data, &feed) != nil {
		return nil
	}
	var out []map[string]any
	for i, item := range feed.Items {
		if i >= 3 {
			break
		}
		out = append(out, map[string]any{
			"source":  "Google News (" + item.Source + ")",
			"title":   cleanHTMLText(item.Title),
			"date":    item.PubDate,
			"url":     item.Link,
		})
	}
	return out
}

func searchWikipedia(client *http.Client, lang, query string) []map[string]any {
	u := fmt.Sprintf(
		"https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&utf8=&format=json&srlimit=2",
		lang,
		url.QueryEscape(query),
	)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "LifelGoStudio/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var payload struct {
		Query struct {
			Search []struct {
				Title   string `json:"title"`
				Snippet string `json:"snippet"`
			} `json:"search"`
		} `json:"query"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 100000))
	if err := jsonUnmarshal(body, &payload); err != nil {
		return nil
	}
	var out []map[string]any
	for _, s := range payload.Query.Search {
		wikiURL := fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, url.PathEscape(strings.ReplaceAll(s.Title, " ", "_")))
		out = append(out, map[string]any{
			"source":  fmt.Sprintf("Wikipedia (%s)", strings.ToUpper(lang)),
			"title":   s.Title,
			"snippet": cleanHTMLText(s.Snippet),
			"url":     wikiURL,
		})
	}
	return out
}

func fetchReadableWebpage(rawURL string) map[string]any {
	client := &http.Client{Timeout: 6 * time.Second}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	defer resp.Body.Close()

	rawBytes, err := io.ReadAll(io.LimitReader(resp.Body, 350000))
	if err != nil {
		return map[string]any{"error": err.Error()}
	}

	cleaned := cleanHTMLText(string(rawBytes))
	totalLen := len(cleaned)
	if len(cleaned) > 4500 {
		cleaned = cleaned[:4500] + "...[truncated]"
	}
	return map[string]any{
		"url":         rawURL,
		"status_code": resp.StatusCode,
		"total_chars": totalLen,
		"content":     cleaned,
	}
}

func jsonUnmarshal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}

// ==============================================================================
// Tavily AI Search, Extraction & Crawling Integration (app.tavily.com)
// ==============================================================================

func searchTavily(apiKey, query, depth string) (map[string]any, error) {
	if depth == "" {
		depth = "advanced"
	}
	reqBody := map[string]any{
		"query":          query,
		"search_depth":   depth,
		"max_results":    5,
		"include_answer": true,
		"include_images": false,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest("POST", "https://api.tavily.com/search", bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tavily status %d: %s", resp.StatusCode, string(b))
	}

	var parsed struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string  `json:"title"`
			URL     string  `json:"url"`
			Content string  `json:"content"`
			Score   float64 `json:"score"`
		} `json:"results"`
		ResponseTime float64 `json:"response_time"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	var results []map[string]any
	for _, r := range parsed.Results {
		content := r.Content
		if len(content) > 1500 {
			content = content[:1500] + "...[truncated]"
		}
		results = append(results, map[string]any{
			"source":  "Tavily AI (Advanced)",
			"title":   r.Title,
			"url":     r.URL,
			"snippet": content,
			"score":   r.Score,
		})
	}

	out := map[string]any{
		"provider":      "Tavily AI",
		"search_depth":  depth,
		"query":         query,
		"count":         len(results),
		"results":       results,
		"response_time": fmt.Sprintf("%.2fs", parsed.ResponseTime),
		"tip":           "Use read_webpage or crawl_website for in-depth source extraction.",
	}
	if parsed.Answer != "" {
		out["direct_answer"] = parsed.Answer
	}
	return out, nil
}

func extractTavily(apiKey string, urls []string) (map[string]any, error) {
	reqBody := map[string]any{
		"urls":          urls,
		"extract_depth": "advanced",
		"format":        "markdown",
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", "https://api.tavily.com/extract", bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tavily extract status %d: %s", resp.StatusCode, string(b))
	}

	var parsed struct {
		Results []struct {
			URL        string `json:"url"`
			Title      string `json:"title"`
			RawContent string `json:"raw_content"`
		} `json:"results"`
		FailedResults []struct {
			URL   string `json:"url"`
			Error string `json:"error"`
		} `json:"failed_results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	if len(parsed.Results) == 0 {
		return nil, fmt.Errorf("no extracted content")
	}

	first := parsed.Results[0]
	content := first.RawContent
	totalChars := len(content)
	if len(content) > 5000 {
		content = content[:5000] + "\n...[truncated]"
	}

	return map[string]any{
		"provider":    "Tavily Extract (Markdown)",
		"url":         first.URL,
		"title":       first.Title,
		"total_chars": totalChars,
		"content":     content,
	}, nil
}

func crawlTavily(apiKey, targetURL, instructions string) (map[string]any, error) {
	reqBody := map[string]any{
		"url":           targetURL,
		"instructions":  instructions,
		"max_depth":     1,
		"limit":         5,
		"extract_depth": "advanced",
		"format":        "markdown",
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", "https://api.tavily.com/crawl", bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tavily crawl status %d: %s", resp.StatusCode, string(b))
	}

	var parsed struct {
		BaseURL string `json:"base_url"`
		Results []struct {
			URL        string `json:"url"`
			RawContent string `json:"raw_content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	var crawled []map[string]any
	for _, r := range parsed.Results {
		snippet := r.RawContent
		if len(snippet) > 800 {
			snippet = snippet[:800] + "...[truncated]"
		}
		crawled = append(crawled, map[string]any{
			"url":     r.URL,
			"snippet": snippet,
		})
	}

	return map[string]any{
		"provider":     "Tavily Crawl",
		"base_url":     parsed.BaseURL,
		"pages_count":  len(crawled),
		"pages":        crawled,
		"instructions": instructions,
	}, nil
}
