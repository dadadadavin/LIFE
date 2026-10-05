package main

import (
	"bufio"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var pageTemplates *template.Template

func initTemplates() error {
	funcMap := template.FuncMap{
		"joinStrings": func(arr []string, sep string) string {
			return strings.Join(arr, sep)
		},
		"voiceGridHTML": func(cfg AppConfig) template.HTML {
			return template.HTML(renderVoiceGridHTML(cfg))
		},
		"memoryListHTML": func() template.HTML {
			return template.HTML(renderMemoryListHTML("", ""))
		},
		"sessionsListHTML": func() template.HTML {
			return template.HTML(renderSessionsListHTML())
		},
		"keysListHTML": func(cfg AppConfig) template.HTML {
			return template.HTML(renderKeysListHTML(cfg))
		},
		"notesListHTML": func() template.HTML {
			return template.HTML(renderNotesListHTML())
		},
	}

	tmplPath := filepath.Join(baseDir, "templates", "index.html")
	t, err := template.New("index.html").Funcs(funcMap).ParseFiles(tmplPath)
	if err != nil {
		return err
	}
	pageTemplates = t
	return nil
}

type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
	hijacked   bool
}

func (w *statusResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		w.hijacked = true
		w.statusCode = http.StatusSwitchingProtocols
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

func (w *statusResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(sw, r)
		duration := time.Since(start)

		// Omit static assets and favicon from terminal logging unless error
		if (strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/favicon.ico") && sw.statusCode < 400 {
			return
		}

		timeStr := time.Now().Format("15:04:05")

		// WebSocket upgrade requests hijack connection (HTTP 101)
		if sw.hijacked || sw.statusCode == http.StatusSwitchingProtocols {
			fmt.Printf("%s [HTTP] %-6s %-28s \033[36m101\033[0m %8s  (%s)\n",
				timeStr, r.Method, r.URL.Path, duration.Truncate(100*time.Microsecond), r.RemoteAddr)
			return
		}

		statusColor := "\033[32m" // green
		if sw.statusCode >= 400 {
			statusColor = "\033[31m" // red
		} else if sw.statusCode >= 300 {
			statusColor = "\033[33m" // yellow
		}
		resetColor := "\033[0m"

		fmt.Printf("%s [HTTP] %-6s %-28s %s%3d%s %8s  (%s)\n",
			timeStr,
			r.Method,
			r.URL.Path,
			statusColor, sw.statusCode, resetColor,
			duration.Truncate(100*time.Microsecond),
			r.RemoteAddr,
		)
	})
}

func main() {
	portFlag := flag.String("port", "", "Server port (defaults to $PORT or 8000)")
	flag.Parse()

	initPaths()

	cfg := loadConfig()
	memories := loadMemories()

	if err := initTemplates(); err != nil {
		log.Fatalf("\033[31m[ERROR] Failed to parse templates: %v\033[0m\n", err)
	}

	port := *portFlag
	if port == "" {
		port = os.Getenv("PORT")
	}
	if port == "" {
		port = "8000"
	}
	addr := "127.0.0.1:" + port

	// Check if port is already bound to provide helpful troubleshooting output
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("\n\033[31m╔══════════════════════════════════════════════════════════════════════════╗\033[0m\n")
		fmt.Printf("\033[31m║ [PORT CONFLICT ERROR] Address %s is already in use!             ║\033[0m\n", addr)
		fmt.Printf("\033[31m╚══════════════════════════════════════════════════════════════════════════╝\033[0m\n\n")
		fmt.Printf("Another process is currently holding port %s.\n", port)
		fmt.Printf("• To terminate it on macOS run:  \033[33mlsof -ti :%s | xargs kill -9\033[0m\n", port)
		fmt.Printf("• Or run Lifel on a different port:  \033[32m./lifel -port 8080\033[0m  or  \033[32mPORT=8080 ./lifel\033[0m\n\n")
		os.Exit(1)
	}

	mux := http.NewServeMux()
	staticDir := filepath.Join(baseDir, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	registerRoutes(mux)

	allKeys := getAllEnvKeys()
	keysCount := len(allKeys)
	if _, ok := allKeys["GEMINI_API_KEY"]; ok {
		keysCount--
	}

	fmt.Println()
	fmt.Printf("\033[1;36m┌────────────────────────────────────────────────────────────────────────┐\033[0m\n")
	fmt.Printf("\033[1;36m│                     Lifel — Live Voice & Vision AI                     │\033[0m\n")
	fmt.Printf("\033[1;36m└────────────────────────────────────────────────────────────────────────┘\033[0m\n")
	fmt.Printf("  • \033[1mWeb UI\033[0m:             \033[32mhttp://%s\033[0m\n", addr)
	fmt.Printf("  • \033[1mAI Model\033[0m:           %s (multimodal bidirectional live)\n", cfg.Model)
	fmt.Printf("  • \033[1mActive Voice\033[0m:       %s\n", cfg.VoiceName)
	fmt.Printf("  • \033[1mAPI Keys\033[0m:           Slot: \033[33m%s\033[0m (%d keys configured in .env)\n", cfg.SelectedKey, keysCount)
	fmt.Printf("  • \033[1mVAD Settings\033[0m:       silence=%dms, end_sensitivity=%s\n", cfg.SilenceDurationMs, cfg.EndSensitivity)
	fmt.Printf("  • \033[1mEnabled Tools (%d)\033[0m:   %s\n", len(cfg.EnabledTools), strings.Join(cfg.EnabledTools, ", "))
	fmt.Printf("  • \033[1mSaved Memories\033[0m:     %d stored items\n", len(memories))
	fmt.Printf("  • \033[1mTerminal Tracing\033[0m:   \033[32mTRANSPARENT (All live events, errors, & tools logged)\033[0m\n")
	fmt.Printf("\033[38;5;244m────────────────────────────────────────────────────────────────────────\033[0m\n")
	fmt.Printf("Ready. Open \033[1mhttp://%s\033[0m in your browser.\n\n", addr)

	server := &http.Server{
		Handler: requestLoggingMiddleware(mux),
	}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("\033[31m[ERROR] Server error: %v\033[0m\n", err)
	}
}
