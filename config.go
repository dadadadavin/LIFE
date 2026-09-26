package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

var (
	baseDir     string
	envPath     string
	configPath  string
	memoryPath  string
	sessionsDir string
	notesDir    string
	staticDir   string

	configMu sync.Mutex
	memoryMu sync.Mutex
	envMu    sync.Mutex
)

func initPaths() {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	baseDir = wd
	envPath = filepath.Join(baseDir, ".env")
	configPath = filepath.Join(baseDir, "config.json")
	memoryPath = filepath.Join(baseDir, "memory.json")
	sessionsDir = filepath.Join(baseDir, "sessions")
	notesDir = filepath.Join(baseDir, "notes")
	staticDir = filepath.Join(baseDir, "static")

	_ = os.MkdirAll(sessionsDir, 0755)
	_ = os.MkdirAll(notesDir, 0755)
	_ = os.MkdirAll(staticDir, 0755)
	_ = godotenv.Load(envPath)
}

func shortID(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	if prefix == "" {
		return hex.EncodeToString(b)[:6]
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

// ==============================================================================
// Configuration (config.json)
// ==============================================================================

type AppConfig struct {
	Model                     string   `json:"model"`
	SelectedKey               string   `json:"selected_key"`
	AutoKeyFailover           bool     `json:"auto_key_failover"`
	VoiceName                 string   `json:"voice_name"`
	Temperature               float32  `json:"temperature"`
	ResponseStyle             string   `json:"response_style"` // "concise", "balanced", "detailed"
	ActivityHandling          string   `json:"activity_handling"`
	EchoShield                bool     `json:"echo_shield"`
	VoiceBargeIn              bool     `json:"voice_barge_in"`
	BargeInThreshold          int      `json:"barge_in_threshold"` // RMS threshold (e.g. 2800)
	StartSensitivity          string   `json:"start_sensitivity"`
	EndSensitivity            string   `json:"end_sensitivity"`
	PrefixPaddingMs           int32    `json:"prefix_padding_ms"`
	SilenceDurationMs         int32    `json:"silence_duration_ms"`
	TurnCoverage              string   `json:"turn_coverage"`
	ProactiveAudio            bool     `json:"proactive_audio"`
	TranscriptionMode         string   `json:"transcription_mode"` // "VERBATIM" or "SMART"
	SttLanguages              string   `json:"stt_languages"`      // e.g. "en-US, id-ID"
	CustomVocabulary          string   `json:"custom_vocabulary"`  // comma-separated terms
	SessionResumption         bool     `json:"session_resumption"`
	ContinueLastSession       bool     `json:"continue_last_session"`
	LastResumptionHandle      string   `json:"last_resumption_handle"`
	ResumeSessionID           string   `json:"resume_session_id"`
	ContextCompression        bool     `json:"context_compression"`
	TriggerTokens             int64    `json:"trigger_tokens"`
	TargetTokens              int64    `json:"target_tokens"`
	VideoFps                  float64  `json:"video_fps"`
	VisionResolution          int      `json:"vision_resolution"` // 640, 1080, 1280
	MirrorCamera              bool     `json:"mirror_camera"`
	BrowserEchoCancellation   bool     `json:"browser_echo_cancellation"`
	BrowserNoiseSuppression   bool     `json:"browser_noise_suppression"`
	BrowserAutoGain           bool     `json:"browser_auto_gain"`
	InjectMemory              bool     `json:"inject_memory"`
	AutoSaveTranscript        bool     `json:"auto_save_transcript"`
	AutoExtractMemory         bool     `json:"auto_extract_memory"`
	ToolBehavior              string   `json:"tool_behavior"`   // "NON_BLOCKING" or "BLOCKING"
	ToolScheduling            string   `json:"tool_scheduling"` // "WHEN_IDLE", "INTERRUPT", "SILENT"
	EnabledTools              []string `json:"enabled_tools"`
	SystemInstruction         string   `json:"system_instruction"`
}

func defaultConfig() AppConfig {
	return AppConfig{
		Model:                   "gemini-3.8-live",
		SelectedKey:             "GEMINI_API_KEY_5",
		AutoKeyFailover:         true,
		VoiceName:               "Puck",
		Temperature:             0.7,
		ResponseStyle:           "concise",
		ActivityHandling:        "NO_INTERRUPTION",
		EchoShield:              true,
		VoiceBargeIn:            true,
		BargeInThreshold:        3200,
		StartSensitivity:        "START_SENSITIVITY_LOW",
		EndSensitivity:          "END_SENSITIVITY_LOW",
		PrefixPaddingMs:         200,
		SilenceDurationMs:       600,
		TurnCoverage:            "TURN_INCLUDES_AUDIO_ACTIVITY_AND_ALL_VIDEO",
		ProactiveAudio:          false,
		TranscriptionMode:       "SMART",
		SttLanguages:            "en-US, id-ID",
		CustomVocabulary:        "Lifel, Gemini 3.8 Live, Samsung Innovation Campus, Indonesia, MacBook Air, Golang, HTMX, Tailwind CSS, KiCad, ESP32",
		SessionResumption:       true,
		ContinueLastSession:     false,
		LastResumptionHandle:    "",
		ResumeSessionID:         "",
		ContextCompression:      true,
		TriggerTokens:           80000,
		TargetTokens:            40000,
		VideoFps:                1.0,
		VisionResolution:        1080,
		MirrorCamera:            false,
		BrowserEchoCancellation: true,
		BrowserNoiseSuppression: true,
		BrowserAutoGain:         true,
		InjectMemory:            true,
		AutoSaveTranscript:      true,
		AutoExtractMemory:       true,
		ToolBehavior:            "NON_BLOCKING",
		ToolScheduling:          "WHEN_IDLE",
		EnabledTools: []string{
			"get_mac_system_info",
			"web_search",
			"read_webpage",
			"save_user_memory",
			"search_user_memory",
			"list_project_files",
			"read_project_file",
			"write_project_note",
			"calculate_math",
			"mac_clipboard",
			"open_url_or_app",
			"capture_mac_screen",
		},
		SystemInstruction: "You are Lifel, a fast, intelligent, and natural real-time voice and vision assistant running natively in Go on the user's Mac. Always respond in the same language the user speaks (English or Indonesian). Keep your spoken answers natural, direct, and easy to listen to. When the user shares their webcam or screen, inspect text, objects, code, or hardware accurately. Use your available tools proactively whenever asked to search the web, read articles, check Mac status, use the clipboard, capture the screen, or save memories.",
	}
}

func loadConfig() AppConfig {
	configMu.Lock()
	defer configMu.Unlock()

	cfg := defaultConfig()
	data, err := os.ReadFile(configPath)
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
		// Ensure new defaults are populated if upgrading from an older config.json
		if cfg.Model == "" {
			cfg.Model = "gemini-3.8-live"
		}
		if cfg.VoiceName == "" {
			cfg.VoiceName = "Puck"
		}
		if cfg.ResponseStyle == "" {
			cfg.ResponseStyle = "concise"
		}
		if cfg.TranscriptionMode == "" {
			cfg.TranscriptionMode = "SMART"
		}
		if cfg.SttLanguages == "" {
			cfg.SttLanguages = "en-US, id-ID"
		}
		if cfg.CustomVocabulary == "" {
			cfg.CustomVocabulary = defaultConfig().CustomVocabulary
		}
		if cfg.VisionResolution == 0 {
			cfg.VisionResolution = 1080
		}
		if cfg.BargeInThreshold == 0 {
			cfg.BargeInThreshold = 3200
		}
		// Ensure new tools are registered if upgrading from 8-tool config
		hasReadWeb := false
		for _, t := range cfg.EnabledTools {
			if t == "read_webpage" {
				hasReadWeb = true
				break
			}
		}
		if !hasReadWeb && len(cfg.EnabledTools) >= 6 {
			for _, extra := range []string{"read_webpage", "mac_clipboard", "open_url_or_app", "capture_mac_screen"} {
				cfg.EnabledTools = append(cfg.EnabledTools, extra)
			}
		}
		return cfg
	}

	b, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(configPath, b, 0644)
	return cfg
}

func saveConfig(cfg AppConfig) AppConfig {
	configMu.Lock()
	defer configMu.Unlock()

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err == nil {
		_ = os.WriteFile(configPath, b, 0644)
	}
	return cfg
}

func saveConfigMap(updates map[string]any) AppConfig {
	current := loadConfig()
	raw, _ := json.Marshal(current)
	var merged map[string]any
	_ = json.Unmarshal(raw, &merged)
	if merged == nil {
		merged = make(map[string]any)
	}
	for k, v := range updates {
		merged[k] = v
	}
	mergedBytes, _ := json.Marshal(merged)
	var updated AppConfig
	_ = json.Unmarshal(mergedBytes, &updated)
	return saveConfig(updated)
}

type VoiceCatalogItem struct {
	Name  string `json:"name"`
	Tone  string `json:"tone"`
	Pitch string `json:"pitch"`
}

var VoiceCatalog = []VoiceCatalogItem{
	{Name: "Puck", Tone: "Upbeat, energetic, friendly", Pitch: "Mid"},
	{Name: "Charon", Tone: "Informative, composed, steady", Pitch: "Low-Mid"},
	{Name: "Kore", Tone: "Firm, clear, articulate", Pitch: "Mid-High"},
	{Name: "Fenrir", Tone: "Excitable, dynamic, expressive", Pitch: "Low"},
	{Name: "Aoede", Tone: "Breezy, warm, conversational", Pitch: "Mid-High"},
	{Name: "Leda", Tone: "Youthful, bright, natural", Pitch: "High"},
	{Name: "Orus", Tone: "Deep, resonant, authoritative", Pitch: "Low"},
	{Name: "Zephyr", Tone: "Calm, gentle, smooth", Pitch: "Mid"},
}

// ==============================================================================
// Persistent Memory Bank (memory.json)
// ==============================================================================

type MemoryItem struct {
	ID        string `json:"id"`
	Category  string `json:"category"` // "user", "preference", "project", "task", "summary"
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	Source    string `json:"source"`
	Pinned    bool   `json:"pinned"`
}

func defaultMemories() []MemoryItem {
	now := time.Now().Format("2006-01-02T15:04:05")
	return []MemoryItem{
		{
			ID:        "mem-default-1",
			Category:  "user",
			Content:   "User works on macOS (Apple Silicon) in the local project workspace.",
			CreatedAt: now,
			Source:    "system",
			Pinned:    true,
		},
		{
			ID:        "mem-default-2",
			Category:  "preference",
			Content:   "User speaks English and Indonesian and prefers direct, concise, practical answers with zero AI slop.",
			CreatedAt: now,
			Source:    "system",
			Pinned:    true,
		},
	}
}

func loadMemories() []MemoryItem {
	memoryMu.Lock()
	defer memoryMu.Unlock()

	data, err := os.ReadFile(memoryPath)
	if err == nil {
		var items []MemoryItem
		if json.Unmarshal(data, &items) == nil {
			return items
		}
	}
	defs := defaultMemories()
	b, _ := json.MarshalIndent(defs, "", "  ")
	_ = os.WriteFile(memoryPath, b, 0644)
	return defs
}

func saveMemories(items []MemoryItem) []MemoryItem {
	memoryMu.Lock()
	defer memoryMu.Unlock()

	// Keep pinned items at the top
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Pinned != items[j].Pinned {
			return items[i].Pinned
		}
		return false
	})

	b, err := json.MarshalIndent(items, "", "  ")
	if err == nil {
		_ = os.WriteFile(memoryPath, b, 0644)
	}
	return items
}

func addMemoryItem(content, category, source string, pinned bool) MemoryItem {
	content = strings.TrimSpace(content)
	category = strings.TrimSpace(category)
	if category == "" {
		category = "fact"
	}
	items := loadMemories()

	// If category == "summary", replace existing rolling last-session summary
	if category == "summary" {
		for i := range items {
			if items[i].Category == "summary" {
				items[i].Content = content
				items[i].CreatedAt = time.Now().Format("2006-01-02T15:04:05")
				items[i].Source = source
				items[i].Pinned = true
				saveMemories(items)
				return items[i]
			}
		}
	}

	// Avoid exact duplicates
	lower := strings.ToLower(content)
	for _, m := range items {
		if strings.ToLower(strings.TrimSpace(m.Content)) == lower {
			return m
		}
	}

	item := MemoryItem{
		ID:        shortID("mem"),
		Category:  category,
		Content:   content,
		CreatedAt: time.Now().Format("2006-01-02T15:04:05"),
		Source:    source,
		Pinned:    pinned,
	}
	items = append([]MemoryItem{item}, items...)
	saveMemories(items)
	return item
}

func updateMemoryItem(id, content, category string, togglePin bool) []MemoryItem {
	items := loadMemories()
	for i := range items {
		if items[i].ID == id {
			if content != "" {
				items[i].Content = strings.TrimSpace(content)
			}
			if category != "" {
				items[i].Category = strings.TrimSpace(category)
			}
			if togglePin {
				items[i].Pinned = !items[i].Pinned
			}
			break
		}
	}
	return saveMemories(items)
}

func deleteMemoryItem(id string) []MemoryItem {
	items := loadMemories()
	filtered := make([]MemoryItem, 0, len(items))
	for _, m := range items {
		if m.ID != id {
			filtered = append(filtered, m)
		}
	}
	return saveMemories(filtered)
}

// BuildFullSystemPrompt injects Runtime Self-Awareness (Update #3), Response Style (Update #8),
// Persistent Memory Bank (Update #10), and Resumed Session Context (Update #5).
func buildFullSystemPrompt(cfg AppConfig, activeKeyName string) string {
	var sb strings.Builder
	sb.WriteString(strings.TrimSpace(cfg.SystemInstruction))

	// Response style directive (Update #8)
	switch cfg.ResponseStyle {
	case "concise":
		sb.WriteString("\n\n[RESPONSE STYLE: CONCISE] Keep spoken answers brief (1 to 2 sentences by default) unless the user asks for a deep explanation.")
	case "detailed":
		sb.WriteString("\n\n[RESPONSE STYLE: DETAILED] Provide thorough, detailed explanations with concrete examples.")
	default:
		sb.WriteString("\n\n[RESPONSE STYLE: BALANCED] Keep spoken answers natural, clear, and moderately paced.")
	}

	// Runtime Self-Awareness Block (Update #3)
	sb.WriteString("\n\n=== RUNTIME SELF-AWARENESS & SYSTEM METADATA ===\n")
	sb.WriteString(fmt.Sprintf("- Assistant Name: Lifel\n"))
	sb.WriteString(fmt.Sprintf("- Exact Base AI Model: %s (Google Gemini 3.8 Live multimodal bidirectional model)\n", cfg.Model))
	sb.WriteString(fmt.Sprintf("- Active Voice Profile: %s\n", cfg.VoiceName))
	sb.WriteString(fmt.Sprintf("- Active API Key Slot: %s\n", activeKeyName))
	sb.WriteString(fmt.Sprintf("- Backend Engine: Go (%s %s/%s) + google.golang.org/genai SDK + HTMX + Tailwind CSS\n", runtime.Version(), runtime.GOOS, runtime.GOARCH))
	sb.WriteString(fmt.Sprintf("- Local Date & Time: %s\n", time.Now().Format("2006-01-02 15:04:05 (Monday)")))
	sb.WriteString(fmt.Sprintf("- Active Speech Languages (Bilingual STT): %s (Transcription Mode: %s)\n", cfg.SttLanguages, cfg.TranscriptionMode))
	if strings.TrimSpace(cfg.CustomVocabulary) != "" {
		sb.WriteString(fmt.Sprintf("- Custom Vocabulary & Domain Terms (recognize these spellings accurately when spoken): %s\n", cfg.CustomVocabulary))
	}
	sb.WriteString(fmt.Sprintf("- Enabled Local Tools (%d): %s\n", len(cfg.EnabledTools), strings.Join(cfg.EnabledTools, ", ")))
	sb.WriteString("- Note: If the user asks what model you are, what voice you use, or how you are built, state these exact facts directly.\n")
	sb.WriteString("================================================")

	// Persistent User Memory Bank (Update #10)
	if cfg.InjectMemory {
		memories := loadMemories()
		if len(memories) > 0 {
			sb.WriteString("\n\n=== PERSISTENT USER MEMORY BANK ===\n")
			for _, m := range memories {
				pinTag := ""
				if m.Pinned {
					pinTag = "[PINNED] "
				}
				sb.WriteString(fmt.Sprintf("- %s[%s] %s\n", pinTag, strings.ToUpper(m.Category), m.Content))
			}
			sb.WriteString("===================================")
		}
	}

	// Resumed Session Context (Update #5)
	if cfg.ResumeSessionID != "" {
		if rec, err := getSessionRecord(cfg.ResumeSessionID); err == nil && len(rec.Turns) > 0 {
			sb.WriteString(fmt.Sprintf("\n\n=== RESUMED CONVERSATION CONTEXT (Session %s from %s) ===\n", rec.ID, rec.StartedAt))
			sb.WriteString("The user is continuing this previous conversation. Pick up naturally where you left off:\n")
			startIdx := 0
			if len(rec.Turns) > 16 {
				startIdx = len(rec.Turns) - 16
			}
			for _, t := range rec.Turns[startIdx:] {
				sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", t.Time, strings.ToUpper(t.Role), t.Text))
			}
			sb.WriteString("=========================================================")
		}
	}

	return sb.String()
}

// ==============================================================================
// API Keys (.env)
// ==============================================================================

type KeyInfo struct {
	Name     string `json:"name"`
	Masked   string `json:"masked"`
	Type     string `json:"type"`
	Selected bool   `json:"selected"`
}

func getAllEnvKeys() map[string]string {
	envMu.Lock()
	defer envMu.Unlock()

	_ = godotenv.Overload(envPath)
	envMap, _ := godotenv.Read(envPath)
	if envMap == nil {
		envMap = make(map[string]string)
	}

	result := make(map[string]string)
	for k, v := range envMap {
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if strings.HasPrefix(k, "GEMINI_API_KEY") && v != "" {
			result[k] = v
		}
	}
	for _, k := range []string{"GEMINI_API_KEY_1", "GEMINI_API_KEY_2", "GEMINI_API_KEY_3", "GEMINI_API_KEY_4", "GEMINI_API_KEY_5"} {
		if _, ok := result[k]; !ok {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				result[k] = v
			}
		}
	}
	return result
}

func maskKey(val string) string {
	if len(val) < 12 {
		return "••••••••"
	}
	return val[:7] + "••••••••••••" + val[len(val)-5:]
}

func listKeyInfos(cfg AppConfig) []KeyInfo {
	all := getAllEnvKeys()
	names := make([]string, 0, len(all))
	for k := range all {
		if k == "GEMINI_API_KEY" {
			continue
		}
		names = append(names, k)
	}
	sort.Strings(names)

	var out []KeyInfo
	for _, k := range names {
		v := all[k]
		kType := "Gemini Cloud Key"
		if strings.HasPrefix(v, "AIza") {
			kType = "Google AI Studio Key"
		}
		out = append(out, KeyInfo{
			Name:     k,
			Masked:   maskKey(v),
			Type:     kType,
			Selected: k == cfg.SelectedKey,
		})
	}
	return out
}

type NamedKey struct {
	Name  string
	Value string
}

func getOrderedAPIKeys(preferred string, autoFailover bool) []NamedKey {
	all := getAllEnvKeys()
	var ordered []NamedKey
	seen := make(map[string]bool)

	if v, ok := all[preferred]; ok && v != "" {
		ordered = append(ordered, NamedKey{Name: preferred, Value: v})
		seen[v] = true
	}

	if autoFailover {
		names := make([]string, 0, len(all))
		for k := range all {
			if k != "GEMINI_API_KEY" {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		for _, k := range names {
			v := all[k]
			if !seen[v] && v != "" {
				ordered = append(ordered, NamedKey{Name: k, Value: v})
				seen[v] = true
			}
		}
	}
	return ordered
}

func saveEnvKey(keyName, keyValue string) error {
	envMu.Lock()
	defer envMu.Unlock()

	envMap, err := godotenv.Read(envPath)
	if err != nil || envMap == nil {
		envMap = make(map[string]string)
	}
	envMap[keyName] = keyValue
	return godotenv.Write(envMap, envPath)
}

// ==============================================================================
// Session History (sessions/*.json & sessions/*.md)
// ==============================================================================

type TurnEntry struct {
	Role      string `json:"role"` // "user", "model", "tool"
	Text      string `json:"text"`
	Time      string `json:"time"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
}

type UsageStats struct {
	PromptTokens   int32 `json:"prompt_tokens"`
	ResponseTokens int32 `json:"response_tokens"`
	TotalTokens    int32 `json:"total_tokens"`
}

type SessionRecord struct {
	ID               string      `json:"id"`
	StartedAt        string      `json:"started_at"`
	DurationSec      float64     `json:"duration_sec"`
	Model            string      `json:"model"`
	Voice            string      `json:"voice"`
	KeyName          string      `json:"key_name"`
	ResumptionHandle string      `json:"resumption_handle,omitempty"`
	Usage            UsageStats  `json:"usage"`
	Turns            []TurnEntry `json:"turns"`
}

type SessionSummaryItem struct {
	ID          string  `json:"id"`
	StartedAt   string  `json:"started_at"`
	DurationSec float64 `json:"duration_sec"`
	Model       string  `json:"model"`
	Voice       string  `json:"voice"`
	KeyName     string  `json:"key_name"`
	TurnCount   int     `json:"turn_count"`
	TotalTokens int32   `json:"total_tokens"`
	Preview     string  `json:"preview"`
}

func saveSessionRecord(rec SessionRecord) error {
	jsonPath := filepath.Join(sessionsDir, rec.ID+".json")
	mdPath := filepath.Join(sessionsDir, rec.ID+".md")

	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, b, 0644); err != nil {
		return err
	}

	var md strings.Builder
	md.WriteString(fmt.Sprintf("# Session Transcript (%s)\n", rec.StartedAt))
	md.WriteString(fmt.Sprintf("- **Session ID**: `%s`\n", rec.ID))
	md.WriteString(fmt.Sprintf("- **Model**: `%s`\n", rec.Model))
	md.WriteString(fmt.Sprintf("- **Voice**: `%s`\n", rec.Voice))
	md.WriteString(fmt.Sprintf("- **Duration**: `%.1fs`\n", rec.DurationSec))
	md.WriteString(fmt.Sprintf("- **Total Tokens**: `%d`\n\n---\n\n## Conversation\n\n", rec.Usage.TotalTokens))

	for _, t := range rec.Turns {
		roleLabel := "**Gemini**"
		if t.Role == "user" {
			roleLabel = "**You**"
		} else if t.Role == "tool" {
			roleLabel = "**Tool**"
		}
		md.WriteString(fmt.Sprintf("### %s _%s_\n%s\n\n", roleLabel, t.Time, t.Text))
	}
	return os.WriteFile(mdPath, []byte(md.String()), 0644)
}

func listSessionSummaries() []SessionSummaryItem {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	var out []SessionSummaryItem
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(sessionsDir, name))
		if err != nil {
			continue
		}
		var rec SessionRecord
		if json.Unmarshal(data, &rec) != nil {
			continue
		}
		preview := "Empty session"
		if len(rec.Turns) > 0 {
			preview = rec.Turns[0].Text
			if len(preview) > 90 {
				preview = preview[:90] + "..."
			}
		}
		out = append(out, SessionSummaryItem{
			ID:          rec.ID,
			StartedAt:   rec.StartedAt,
			DurationSec: rec.DurationSec,
			Model:       rec.Model,
			Voice:       rec.Voice,
			KeyName:     rec.KeyName,
			TurnCount:   len(rec.Turns),
			TotalTokens: rec.Usage.TotalTokens,
			Preview:     preview,
		})
	}
	return out
}

func getSessionRecord(id string) (SessionRecord, error) {
	cleanID := filepath.Base(id)
	data, err := os.ReadFile(filepath.Join(sessionsDir, cleanID+".json"))
	if err != nil {
		return SessionRecord{}, err
	}
	var rec SessionRecord
	err = json.Unmarshal(data, &rec)
	return rec, err
}

func deleteSessionRecord(id string) {
	cleanID := filepath.Base(id)
	_ = os.Remove(filepath.Join(sessionsDir, cleanID+".json"))
	_ = os.Remove(filepath.Join(sessionsDir, cleanID+".md"))
}
