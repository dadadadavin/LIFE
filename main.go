package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	}

	tmplPath := filepath.Join(baseDir, "templates", "index.html")
	t, err := template.New("index.html").Funcs(funcMap).ParseFiles(tmplPath)
	if err != nil {
		return err
	}
	pageTemplates = t
	return nil
}

func main() {
	initPaths()

	// Ensure default config & memory files exist
	_ = loadConfig()
	_ = loadMemories()

	if err := initTemplates(); err != nil {
		log.Fatalf("Failed to parse templates: %v", err)
	}

	mux := http.NewServeMux()

	staticDir := filepath.Join(baseDir, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	registerRoutes(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}
	addr := "127.0.0.1:" + port
	fmt.Printf("Lifel Go + HTMX + Tailwind CSS server listening on http://%s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
