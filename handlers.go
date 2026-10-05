package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genai"
)

type ToolView struct {
	ToolSpec
	Enabled bool `json:"enabled"`
}

type PageData struct {
	Config    AppConfig
	Voices    []VoiceCatalogItem
	Memories  []MemoryItem
	Tools     []ToolView
	Sessions  []SessionSummaryItem
	Keys      []KeyInfo
	Notes     []NoteItem
	BuildTime int64
}

func buildToolViews(cfg AppConfig) []ToolView {
	enabledSet := map[string]bool{}
	for _, name := range cfg.EnabledTools {
		enabledSet[name] = true
	}
	var out []ToolView
	for _, spec := range toolCatalog {
		out = append(out, ToolView{
			ToolSpec: spec,
			Enabled:  enabledSet[spec.Name],
		})
	}
	return out
}

func buildPageData() PageData {
	cfg := loadConfig()
	return PageData{
		Config:    cfg,
		Voices:    VoiceCatalog,
		Memories:  loadMemories(),
		Tools:     buildToolViews(cfg),
		Sessions:  listSessionSummaries(),
		Keys:      listKeyInfos(cfg),
		Notes:     listNotes(),
		BuildTime: time.Now().UnixNano(),
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

// ============================================================================
// HTMX Fragment Renderers
// ============================================================================

func renderVoiceGridHTML(cfg AppConfig) string {
	var sb strings.Builder
	for _, v := range VoiceCatalog {
		active := v.Name == cfg.VoiceName
		borderCls := "border-[#e4e4df] bg-white hover:border-[#b8b8b0]"
		badgeHTML := `<span class="text-[11px] text-[#666660] px-2 py-0.5 rounded bg-[#f4f4f0] border border-[#e4e4df]">Select</span>`
		if active {
			borderCls = "border-[#1a1a19] bg-[#e8f0f8]"
			badgeHTML = `<span class="text-[11px] font-medium px-2 py-0.5 rounded bg-[#1a1a19] text-white">Selected</span>`
		}
		sb.WriteString(fmt.Sprintf(`
		<button type="button"
			hx-post="/htmx/voice/select"
			hx-vals='{"voice_name": %q}'
			hx-target="#voice-grid-container"
			hx-swap="innerHTML"
			class="text-left p-3.5 rounded-md border %s transition-colors flex flex-col justify-between gap-2 cursor-pointer">
			<div class="flex items-center justify-between w-full">
				<span class="font-semibold text-[14px] text-[#1a1a19]">%s</span>
				%s
			</div>
			<div class="text-[12px] text-[#666660]">%s · %s</div>
		</button>`,
			v.Name, borderCls, html.EscapeString(v.Name), badgeHTML,
			html.EscapeString(v.Tone), html.EscapeString(v.Pitch)))
	}
	return sb.String()
}

func renderMemoryListHTML(query string, catFilter string) string {
	items := loadMemories()
	q := strings.ToLower(strings.TrimSpace(query))
	cat := strings.ToLower(strings.TrimSpace(catFilter))

	var filtered []MemoryItem
	for _, m := range items {
		if cat != "" && cat != "all" && strings.ToLower(m.Category) != cat {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(m.Content), q) && !strings.Contains(strings.ToLower(m.Category), q) {
			continue
		}
		filtered = append(filtered, m)
	}

	if len(filtered) == 0 {
		return `<div class="p-6 text-center text-[13px] text-[#666660] bg-[#f9f9f7] rounded-md border border-[#e4e4df]">No memories match your filter. Add a fact above or talk with Gemini during a live session.</div>`
	}

	var sb strings.Builder
	for _, m := range filtered {
		bgCls := "bg-white border-[#e4e4df]"
		catBadgeCls := "bg-[#f4f4f0] text-[#555550] border-[#e4e4df]"
		if m.Pinned {
			bgCls = "bg-[#fdfcf7] border-[#d8d0b8]"
		}
		if m.Category == "summary" {
			bgCls = "bg-[#e8f0f8]/50 border-[#c9d9eb]"
			catBadgeCls = "bg-[#e8f0f8] text-[#1f4b7a] border-[#b8cee6]"
		} else if m.Category == "preference" {
			catBadgeCls = "bg-[#e6f3ec] text-[#1f5c3a] border-[#bfe0ce]"
		} else if m.Category == "project" {
			catBadgeCls = "bg-[#f0ecf8] text-[#453270] border-[#d6cceb]"
		}

		pinLabel := "Pin"
		pinBtnCls := "text-[#666660] hover:text-[#1a1a19] bg-[#f9f9f7]"
		if m.Pinned {
			pinLabel = "Pinned"
			pinBtnCls = "text-[#1f4b7a] bg-[#e8f0f8] border-[#b8cee6] font-medium"
		}

		dateStr := m.CreatedAt
		if len(dateStr) >= 16 {
			dateStr = strings.ReplaceAll(dateStr[:16], "T", " ")
		}

		sb.WriteString(fmt.Sprintf(`
		<div class="p-3.5 rounded-md border %s flex flex-col gap-2" id="mem-row-%s">
			<div class="flex items-start justify-between gap-3">
				<div class="flex-1">
					<div class="flex items-center gap-2 mb-1 flex-wrap">
						<span class="text-[11px] px-2 py-0.5 rounded border %s uppercase tracking-wide">%s</span>
						<span class="text-[11px] text-[#888880] font-mono">#%s · %s · %s</span>
					</div>
					<p class="text-[13.5px] text-[#1a1a19] leading-relaxed" id="mem-text-%s">%s</p>
				</div>
				<div class="flex items-center gap-1.5 shrink-0">
					<button type="button"
						hx-post="/htmx/memory/pin"
						hx-vals='{"id": %q}'
						hx-target="#memory-list-container"
						hx-swap="innerHTML"
						class="text-[11px] px-2 py-1 rounded border border-[#e4e4df] %s cursor-pointer">%s</button>
					<button type="button"
						onclick="document.getElementById('mem-edit-%s').classList.toggle('hidden')"
						class="text-[11px] px-2 py-1 rounded border border-[#e4e4df] bg-[#f9f9f7] text-[#444440] hover:text-[#1a1a19] cursor-pointer">Edit</button>
					<button type="button"
						hx-delete="/htmx/memory/%s"
						hx-target="#memory-list-container"
						hx-swap="innerHTML"
						class="text-[11px] px-2 py-1 rounded border border-[#f2cbc6] bg-[#fae8e6] text-[#8a261d] hover:bg-[#f5d5d0] cursor-pointer">Delete</button>
				</div>
			</div>
			<form id="mem-edit-%s" class="hidden pt-2 mt-1 border-t border-[#e4e4df] flex flex-wrap items-center gap-2"
				hx-post="/htmx/memory/update"
				hx-target="#memory-list-container"
				hx-swap="innerHTML">
				<input type="hidden" name="id" value="%s" />
				<input type="text" name="content" value="%s"
					class="flex-1 min-w-[240px] px-2.5 py-1.5 text-[13px] rounded border border-[#d4d4ce] bg-white text-[#1a1a19]" />
				<select name="category" class="px-2.5 py-1.5 text-[12px] rounded border border-[#d4d4ce] bg-white text-[#1a1a19]">
					<option value="fact" %s>fact</option>
					<option value="preference" %s>preference</option>
					<option value="project" %s>project</option>
					<option value="user" %s>user</option>
					<option value="task" %s>task</option>
					<option value="summary" %s>summary</option>
				</select>
				<button type="submit" class="px-3 py-1.5 text-[12px] font-medium rounded bg-[#1a1a19] text-white cursor-pointer">Save</button>
			</form>
		</div>`,
			bgCls, m.ID,
			catBadgeCls, html.EscapeString(m.Category),
			html.EscapeString(m.ID), html.EscapeString(dateStr), html.EscapeString(m.Source),
			m.ID, html.EscapeString(m.Content),
			m.ID, pinBtnCls, pinLabel,
			m.ID,
			m.ID,
			m.ID, m.ID, html.EscapeString(m.Content),
			selectedAttr(m.Category == "fact"),
			selectedAttr(m.Category == "preference"),
			selectedAttr(m.Category == "project"),
			selectedAttr(m.Category == "user"),
			selectedAttr(m.Category == "task"),
			selectedAttr(m.Category == "summary"),
		))
	}
	return sb.String()
}

func selectedAttr(cond bool) string {
	if cond {
		return "selected"
	}
	return ""
}

func renderSessionsListHTML() string {
	sessions := listSessionSummaries()
	if len(sessions) == 0 {
		return `<div class="p-4 text-[12.5px] text-[#666660] bg-[#f9f9f7] rounded border border-[#e4e4df]">No past sessions recorded yet.</div>`
	}
	var sb strings.Builder
	for _, s := range sessions {
		sb.WriteString(fmt.Sprintf(`
		<div class="p-3 rounded-md border border-[#e4e4df] bg-white hover:bg-[#f9f9f7] transition-colors flex flex-col gap-1.5">
			<div class="flex items-center justify-between gap-2">
				<button type="button"
					hx-get="/htmx/sessions/%s"
					hx-target="#session-detail-container"
					hx-swap="innerHTML"
					class="text-left font-mono text-[12.5px] font-semibold text-[#1a1a19] hover:underline cursor-pointer">
					%s
				</button>
				<div class="flex items-center gap-1">
					<button type="button"
						hx-post="/htmx/sessions/%s/resume"
						hx-target="#resume-banner-slot"
						hx-swap="innerHTML"
						class="text-[11px] px-2 py-0.5 rounded bg-[#e8f0f8] text-[#1f4b7a] border border-[#b8cee6] hover:bg-[#d8e6f3] cursor-pointer"
						title="Resume this conversation in Studio">Resume</button>
					<button type="button"
						hx-delete="/htmx/sessions/%s"
						hx-target="#sessions-list-container"
						hx-swap="innerHTML"
						class="text-[11px] px-1.5 py-0.5 rounded text-[#8a261d] hover:bg-[#fae8e6] cursor-pointer">Del</button>
				</div>
			</div>
			<div class="text-[11.5px] text-[#666660] flex items-center gap-2 flex-wrap">
				<span>Voice: %s</span>
				<span>·</span>
				<span>%d turns</span>
				<span>·</span>
				<span>%.1fs</span>
			</div>
			<div class="text-[11.5px] text-[#777770] truncate">%s</div>
		</div>`,
			html.EscapeString(s.ID),
			html.EscapeString(s.ID),
			html.EscapeString(s.ID),
			html.EscapeString(s.ID),
			html.EscapeString(s.Voice),
			s.TurnCount,
			s.DurationSec,
			html.EscapeString(s.Preview),
		))
	}
	return sb.String()
}

func renderSessionDetailHTML(sessionID string) string {
	rec, err := getSessionRecord(sessionID)
	if err != nil || rec.ID == "" {
		return `<div class="p-6 text-[13px] text-[#666660]">Session not found or deleted.</div>`
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`
	<div class="flex flex-col gap-4">
		<div class="flex items-center justify-between border-b border-[#e4e4df] pb-3 flex-wrap gap-2">
			<div>
				<h3 class="text-[15px] font-semibold text-[#1a1a19] font-mono">Session %s</h3>
				<p class="text-[12px] text-[#666660]">Model: %s · Voice: %s · Key: %s · Turns: %d · Duration: %.1fs · Tokens: %d</p>
			</div>
			<div class="flex items-center gap-2 flex-wrap">
				<a href="/api/sessions/%s/export?format=md" download="session_%s.md"
					class="px-2.5 py-1.5 text-[12px] rounded border border-[#d4d4ce] bg-[#f9f9f7] hover:bg-[#f0f0ec] text-[#1a1a19]">
					Export .md
				</a>
				<a href="/api/sessions/%s/export?format=json" download="session_%s.json"
					class="px-2.5 py-1.5 text-[12px] rounded border border-[#d4d4ce] bg-[#f9f9f7] hover:bg-[#f0f0ec] text-[#1a1a19]">
					Export .json
				</a>
				<button type="button"
					hx-post="/htmx/sessions/%s/resume"
					hx-target="#resume-banner-slot"
					hx-swap="innerHTML"
					class="px-3 py-1.5 text-[12.5px] font-medium rounded bg-[#e8f0f8] text-[#1f4b7a] border border-[#b8cee6] hover:bg-[#d8e6f3] cursor-pointer">
					Resume in Live Studio
				</button>
			</div>
		</div>`,
		html.EscapeString(rec.ID),
		html.EscapeString(rec.Model),
		html.EscapeString(rec.Voice),
		html.EscapeString(rec.KeyName),
		len(rec.Turns),
		rec.DurationSec,
		rec.Usage.TotalTokens,
		html.EscapeString(rec.ID),
		html.EscapeString(rec.ID),
		html.EscapeString(rec.ID),
		html.EscapeString(rec.ID),
		html.EscapeString(rec.ID),
	))

	sb.WriteString(`<div class="flex flex-col gap-2"><div class="text-[12px] font-semibold text-[#444440]">Conversation &amp; Tool Log</div>`)
	if len(rec.Turns) == 0 {
		sb.WriteString(`<div class="p-4 text-[12.5px] text-[#666660] bg-[#f9f9f7] rounded border border-[#e4e4df]">No turns recorded in this session.</div>`)
	} else {
		for _, t := range rec.Turns {
			if t.Role == "tool" {
				sb.WriteString(fmt.Sprintf(`
				<details class="rounded border border-[#d6cceb] bg-[#f0ecf8]/50 px-3 py-2 text-[12px]">
					<summary class="cursor-pointer font-mono font-medium text-[#3b2a63] flex items-center justify-between">
						<span class="truncate max-w-[460px]">Tool Call</span>
						<span class="text-[11px] text-[#666660]">%s</span>
					</summary>
					<pre class="mt-2 p-2 rounded bg-white border border-[#e4e4df] text-[11px] font-mono overflow-x-auto whitespace-pre-wrap text-[#1a1a19]">%s</pre>
				</details>`,
					html.EscapeString(t.Time),
					html.EscapeString(t.Text),
				))
				continue
			}

			roleBadge := `<span class="text-[11px] font-semibold uppercase tracking-wider text-[#1f4b7a]">You</span>`
			boxCls := "bg-[#e8f0f8]/60 border-[#c9d9eb]"
			if t.Role == "model" || t.Role == "gemini" {
				roleBadge = `<span class="text-[11px] font-semibold uppercase tracking-wider text-[#1a1a19]">Gemini</span>`
				boxCls = "bg-white border-[#e4e4df]"
			}
			latBadge := ""
			if t.LatencyMs > 0 {
				latBadge = fmt.Sprintf(`<span class="text-[10.5px] font-mono px-1.5 py-0.5 rounded bg-[#f4f4f0] text-[#555550] border border-[#e4e4df]">%dms</span>`, t.LatencyMs)
			}
			sb.WriteString(fmt.Sprintf(`
			<div class="p-3 rounded-md border %s">
				<div class="flex items-center justify-between mb-1">
					%s
					<div class="flex items-center gap-2">
						%s
						<span class="text-[11px] font-mono text-[#777770]">%s</span>
					</div>
				</div>
				<div class="text-[13.5px] text-[#1a1a19] leading-relaxed whitespace-pre-wrap">%s</div>
			</div>`,
				boxCls, roleBadge, latBadge, html.EscapeString(t.Time), html.EscapeString(t.Text),
			))
		}
	}
	sb.WriteString(`</div></div>`)
	return sb.String()
}

func renderNotesListHTML() string {
	notes := listNotes()
	if len(notes) == 0 {
		return `<div class="p-4 text-[12.5px] text-[#666660] bg-[#f9f9f7] rounded border border-[#e4e4df]">No markdown notes in notes/ yet. Create one above or ask Gemini to write a project note.</div>`
	}
	var sb strings.Builder
	for _, n := range notes {
		sb.WriteString(fmt.Sprintf(`
		<details class="rounded-md border border-[#e4e4df] bg-white p-3.5 text-[12.5px]">
			<summary class="cursor-pointer flex items-center justify-between gap-2 font-medium text-[#1a1a19]">
				<span class="font-mono text-[13px] font-semibold">%s</span>
				<span class="text-[11px] text-[#666660] font-mono">%s · %d bytes</span>
			</summary>
			<form hx-post="/htmx/notes/save" hx-target="#notes-list-container" hx-swap="innerHTML" class="mt-3 flex flex-col gap-2">
				<input type="hidden" name="name" value="%s" />
				<textarea name="content" rows="6" class="w-full p-2.5 rounded border border-[#d4d4ce] bg-[#f9f9f7] focus:bg-white font-mono text-[12px] text-[#1a1a19]">%s</textarea>
				<div class="flex items-center justify-between">
					<button type="button"
						hx-delete="/htmx/notes/%s"
						hx-target="#notes-list-container"
						hx-swap="innerHTML"
						class="px-2.5 py-1 rounded border border-[#f2cbc6] bg-[#fae8e6] text-[#8a261d] text-[11.5px] cursor-pointer">
						Delete Note
					</button>
					<button type="submit" class="px-3 py-1 rounded bg-[#1a1a19] text-white text-[12px] font-medium cursor-pointer">
						Save Changes
					</button>
				</div>
			</form>
		</details>`,
			html.EscapeString(n.Name),
			html.EscapeString(n.UpdatedAt),
			n.SizeBytes,
			html.EscapeString(n.Name),
			html.EscapeString(n.Content),
			html.EscapeString(n.Name),
		))
	}
	return sb.String()
}

func renderKeysListHTML(cfg AppConfig) string {
	keys := listKeyInfos(cfg)
	var sb strings.Builder

	autoCls := "border-[#e4e4df] bg-white"
	autoBadge := `<span class="text-[11px] px-2 py-0.5 rounded bg-[#f4f4f0] text-[#555550] border border-[#e4e4df]">Enable Auto Failover</span>`
	if cfg.AutoKeyFailover {
		autoCls = "border-[#1a1a19] bg-[#e6f3ec]"
		autoBadge = `<span class="text-[11px] px-2 py-0.5 rounded bg-[#1a1a19] text-white font-medium">Failover Enabled</span>`
	}

	nextAuto := "true"
	if cfg.AutoKeyFailover {
		nextAuto = "false"
	}

	sb.WriteString(fmt.Sprintf(`
	<div class="p-3.5 rounded-md border %s flex items-center justify-between gap-3">
		<div>
			<div class="text-[13.5px] font-semibold text-[#1a1a19]">Automatic Key Failover Rotation</div>
			<div class="text-[12px] text-[#666660]">Starts with your preferred key below and automatically rotates to fallback keys if quota is exceeded.</div>
		</div>
		<button type="button"
			hx-post="/htmx/keys/select"
			hx-vals='{"auto_key_failover": %q}'
			hx-target="#keys-list-container"
			hx-swap="innerHTML"
			class="shrink-0 cursor-pointer">%s</button>
	</div>`, autoCls, nextAuto, autoBadge))

	for _, k := range keys {
		rowCls := "border-[#e4e4df] bg-white"
		selBtn := fmt.Sprintf(`
		<button type="button"
			hx-post="/htmx/keys/select"
			hx-vals='{"selected_key": %q}'
			hx-target="#keys-list-container"
			hx-swap="innerHTML"
			class="text-[11.5px] px-2.5 py-1 rounded border border-[#d4d4ce] bg-[#f9f9f7] hover:bg-[#e8f0f8] text-[#1a1a19] cursor-pointer">
			Set Primary
		</button>`, k.Name)

		if k.Selected {
			rowCls = "border-[#1a1a19] bg-[#e8f0f8]"
			selBtn = `<span class="text-[11.5px] px-2.5 py-1 rounded bg-[#1a1a19] text-white font-medium">Primary Key</span>`
		}

		sb.WriteString(fmt.Sprintf(`
		<div class="p-3.5 rounded-md border %s flex items-center justify-between gap-3 flex-wrap">
			<div class="flex flex-col gap-0.5">
				<span class="font-mono text-[13px] font-semibold text-[#1a1a19]">%s</span>
				<span class="font-mono text-[12px] text-[#666660]">%s · %s</span>
			</div>
			<div class="flex items-center gap-2">
				<div id="key-test-res-%s"></div>
				<button type="button"
					hx-post="/htmx/keys/test"
					hx-vals='{"name": %q}'
					hx-target="#key-test-res-%s"
					hx-swap="innerHTML"
					class="text-[11.5px] px-2.5 py-1 rounded border border-[#d4d4ce] bg-white hover:bg-[#f4f4f0] text-[#1a1a19] cursor-pointer">
					Test Key
				</button>
				%s
			</div>
		</div>`,
			rowCls,
			html.EscapeString(k.Name),
			html.EscapeString(k.Masked),
			html.EscapeString(k.Type),
			html.EscapeString(k.Name),
			k.Name,
			html.EscapeString(k.Name),
			selBtn,
		))
	}
	return sb.String()
}

// ============================================================================
// Route Registration
// ============================================================================

func registerRoutes(mux *http.ServeMux) {
	// 1. Index Page
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data := buildPageData()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := pageTemplates.ExecuteTemplate(w, "index.html", data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	// 2. Live WebSocket Endpoint
	mux.HandleFunc("GET /ws/live", handleLiveWebSocket)

	// 3. HTMX Endpoints
	mux.HandleFunc("POST /htmx/config", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		updates := map[string]any{}

		for key, vals := range r.Form {
			if len(vals) == 0 {
				continue
			}
			val := vals[len(vals)-1]
			switch key {
			case "model", "voice_name", "response_style", "system_instruction",
				"activity_handling", "turn_coverage", "start_sensitivity", "end_sensitivity",
				"tool_scheduling", "tool_behavior", "selected_key", "custom_vocabulary",
				"transcription_mode", "stt_languages", "resume_session_id":
				updates[key] = val
			case "temperature", "video_fps":
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					updates[key] = f
				}
			case "prefix_padding_ms", "silence_duration_ms", "trigger_tokens", "target_tokens",
				"noise_gate_rms", "barge_in_threshold", "vision_resolution", "speaker_volume":
				if n, err := strconv.Atoi(val); err == nil {
					updates[key] = n
				}
			case "proactive_audio", "echo_shield", "voice_barge_in", "auto_extract_memory",
				"mirror_camera", "auto_key_failover", "inject_memory", "push_to_talk",
				"auto_reconnect", "continue_last_session", "browser_auto_gain",
				"browser_echo_cancellation", "browser_noise_suppression":
				updates[key] = (val == "true" || val == "on" || val == "1")
			}
		}

		_ = saveConfigMap(updates)
		fmt.Printf("%s \033[36m[CONFIG]\033[0m Updated %d settings in config.json\n", time.Now().Format("15:04:05"), len(updates))
		w.Header().Set("HX-Trigger", "configUpdated")
		writeHTML(w, `<span class="inline-flex items-center px-2.5 py-1 rounded text-[12px] font-medium bg-[#e6f3ec] text-[#1f5c3a] border border-[#bfe0ce]">Saved to config.json</span>`)
	})

	mux.HandleFunc("POST /htmx/voice/select", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		vName := strings.TrimSpace(r.FormValue("voice_name"))
		if vName != "" {
			_ = saveConfigMap(map[string]any{"voice_name": vName})
			fmt.Printf("%s \033[36m[VOICE]\033[0m Selected voice profile: %s\n", time.Now().Format("15:04:05"), vName)
		}
		cfg := loadConfig()
		w.Header().Set("HX-Trigger", "configUpdated")
		writeHTML(w, renderVoiceGridHTML(cfg))
	})

	mux.HandleFunc("GET /htmx/memory", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		cat := r.URL.Query().Get("category")
		writeHTML(w, renderMemoryListHTML(q, cat))
	})

	mux.HandleFunc("POST /htmx/memory/add", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		content := strings.TrimSpace(r.FormValue("content"))
		if content == "" {
			content = strings.TrimSpace(r.FormValue("fact"))
		}
		cat := strings.TrimSpace(r.FormValue("category"))
		pinned := r.FormValue("pinned") == "true" || r.FormValue("pinned") == "on"
		if content != "" {
			addMemoryItem(content, cat, "manual", pinned)
			fmt.Printf("%s \033[35m[MEMORY]\033[0m Stored new fact [%s]: %s\n", time.Now().Format("15:04:05"), cat, content)
		}
		writeHTML(w, renderMemoryListHTML("", ""))
	})

	mux.HandleFunc("POST /htmx/memory/update", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := strings.TrimSpace(r.FormValue("id"))
		content := strings.TrimSpace(r.FormValue("content"))
		if content == "" {
			content = strings.TrimSpace(r.FormValue("fact"))
		}
		cat := strings.TrimSpace(r.FormValue("category"))
		if id != "" && content != "" {
			updateMemoryItem(id, content, cat, false)
		}
		writeHTML(w, renderMemoryListHTML("", ""))
	})

	mux.HandleFunc("POST /htmx/memory/pin", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := strings.TrimSpace(r.FormValue("id"))
		if id != "" {
			updateMemoryItem(id, "", "", true)
		}
		writeHTML(w, renderMemoryListHTML("", ""))
	})

	mux.HandleFunc("DELETE /htmx/memory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		deleteMemoryItem(id)
		writeHTML(w, renderMemoryListHTML("", ""))
	})

	mux.HandleFunc("POST /htmx/memory/clear", func(w http.ResponseWriter, r *http.Request) {
		_ = saveMemories([]MemoryItem{})
		writeHTML(w, renderMemoryListHTML("", ""))
	})

	mux.HandleFunc("POST /htmx/tools/toggle", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		enabled := r.Form["enabled_tools"]
		if enabled == nil {
			enabled = []string{}
		}
		_ = saveConfigMap(map[string]any{"enabled_tools": enabled})
		w.Header().Set("HX-Trigger", "configUpdated")
		writeHTML(w, fmt.Sprintf(`<span class="inline-flex items-center px-2.5 py-1 rounded text-[12px] font-medium bg-[#e6f3ec] text-[#1f5c3a] border border-[#bfe0ce]">%d tools active</span>`, len(enabled)))
	})

	mux.HandleFunc("POST /htmx/tools/test", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		name := strings.TrimSpace(r.FormValue("name"))
		rawArgs := strings.TrimSpace(r.FormValue("args"))
		argsMap := map[string]any{}
		if rawArgs != "" {
			if err := json.Unmarshal([]byte(rawArgs), &argsMap); err != nil {
				writeHTML(w, fmt.Sprintf(`<pre class="p-3 rounded bg-[#fae8e6] border border-[#f2cbc6] text-[#8a261d] text-[12px] font-mono">Invalid JSON arguments: %s</pre>`, html.EscapeString(err.Error())))
				return
			}
		}
		res := executeLocalTool(name, argsMap)
		delete(res, "_native_screen_b64")
		pretty, _ := json.MarshalIndent(res, "", "  ")
		writeHTML(w, fmt.Sprintf(`<pre class="p-3 rounded bg-white border border-[#e4e4df] text-[#1a1a19] text-[12px] font-mono overflow-x-auto max-h-[320px]">%s</pre>`, html.EscapeString(string(pretty))))
	})

	mux.HandleFunc("GET /htmx/sessions", func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, renderSessionsListHTML())
	})

	mux.HandleFunc("GET /htmx/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		writeHTML(w, renderSessionDetailHTML(id))
	})

	mux.HandleFunc("POST /htmx/sessions/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_ = saveConfigMap(map[string]any{"resume_session_id": id})
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"resumeSession": %q, "configUpdated": true}`, id))
		writeHTML(w, fmt.Sprintf(`
		<div class="px-3 py-2 rounded-md bg-[#e8f0f8] border border-[#b8cee6] text-[#1f4b7a] text-[12.5px] flex items-center justify-between gap-2">
			<span>Context queued from session <strong class="font-mono">%s</strong>. Click <strong>Start Live Session</strong> to continue that conversation.</span>
			<button type="button" onclick="this.parentElement.remove()" class="text-[11px] underline cursor-pointer">Dismiss</button>
		</div>`, html.EscapeString(id)))
	})

	mux.HandleFunc("DELETE /htmx/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		deleteSessionRecord(id)
		writeHTML(w, renderSessionsListHTML())
	})

	mux.HandleFunc("GET /htmx/notes", func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, renderNotesListHTML())
	})

	mux.HandleFunc("POST /htmx/notes/save", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = strings.TrimSpace(r.FormValue("filename"))
		}
		content := r.FormValue("content")
		mode := strings.TrimSpace(r.FormValue("mode"))
		if name != "" {
			if mode == "append" {
				_, _ = executeLocalTool("write_project_note", map[string]any{
					"title":   name,
					"content": content,
					"mode":    "append",
				})["ok"].(bool)
			} else {
				_ = saveNote(name, content)
			}
		}
		writeHTML(w, renderNotesListHTML())
	})

	mux.HandleFunc("DELETE /htmx/notes/{name}", func(w http.ResponseWriter, r *http.Request) {
		deleteNote(r.PathValue("name"))
		writeHTML(w, renderNotesListHTML())
	})

	mux.HandleFunc("GET /htmx/keys", func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, renderKeysListHTML(loadConfig()))
	})

	mux.HandleFunc("POST /htmx/keys/select", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		updates := map[string]any{}
		if sel := strings.TrimSpace(r.FormValue("selected_key")); sel != "" {
			updates["selected_key"] = sel
		}
		if af := strings.TrimSpace(r.FormValue("auto_key_failover")); af != "" {
			updates["auto_key_failover"] = (af == "true" || af == "1" || af == "on")
		}
		if len(updates) > 0 {
			_ = saveConfigMap(updates)
		}
		w.Header().Set("HX-Trigger", "configUpdated")
		writeHTML(w, renderKeysListHTML(loadConfig()))
	})

	mux.HandleFunc("POST /htmx/keys/add", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		name := strings.TrimSpace(r.FormValue("name"))
		val := strings.TrimSpace(r.FormValue("value"))
		if name != "" && val != "" {
			_ = saveEnvKey(name, val)
		}
		writeHTML(w, renderKeysListHTML(loadConfig()))
	})

	mux.HandleFunc("POST /htmx/keys/test", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		name := strings.TrimSpace(r.FormValue("name"))
		envKeys := getAllEnvKeys()
		val := envKeys[name]
		if val == "" {
			writeHTML(w, `<span class="text-[11px] px-2 py-0.5 rounded bg-[#fae8e6] text-[#8a261d]">Not found</span>`)
			return
		}
		ok, latencyMs, msg := verifyAPIKey(val)
		if ok {
			writeHTML(w, fmt.Sprintf(`<span class="text-[11px] font-mono px-2 py-0.5 rounded bg-[#e6f3ec] text-[#1f5c3a] border border-[#bfe0ce]">Valid (%dms)</span>`, latencyMs))
		} else {
			writeHTML(w, fmt.Sprintf(`<span class="text-[11px] px-2 py-0.5 rounded bg-[#fae8e6] text-[#8a261d] border border-[#f2cbc6]" title="%s">Error</span>`, html.EscapeString(msg)))
		}
	})

	// ========================================================================
	// 4. JSON REST API Endpoints (for E2E testing & JS state sync)
	// ========================================================================

	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		cfg := loadConfig()
		writeJSON(w, http.StatusOK, map[string]any{
			"config":   cfg,
			"voices":   VoiceCatalog,
			"memories": loadMemories(),
			"tools":    buildToolViews(cfg),
			"keys":     listKeyInfos(cfg),
			"sessions": listSessionSummaries(),
			"notes":    listNotes(),
		})
	})

	mux.HandleFunc("POST /api/config", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		cfg := saveConfigMap(body)
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "config": cfg})
	})

	mux.HandleFunc("GET /api/memory", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"memories": loadMemories()})
	})

	mux.HandleFunc("POST /api/memory", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content  string `json:"content"`
			Fact     string `json:"fact"`
			Category string `json:"category"`
			Pinned   bool   `json:"pinned"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		text := strings.TrimSpace(body.Content)
		if text == "" {
			text = strings.TrimSpace(body.Fact)
		}
		if text == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "content is required"})
			return
		}
		item := addMemoryItem(text, body.Category, "manual", body.Pinned)
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "item": item, "memories": loadMemories()})
	})

	mux.HandleFunc("PUT /api/memory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var body struct {
			Content   string `json:"content"`
			Fact      string `json:"fact"`
			Category  string `json:"category"`
			TogglePin bool   `json:"toggle_pin"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		text := strings.TrimSpace(body.Content)
		if text == "" {
			text = strings.TrimSpace(body.Fact)
		}
		items := updateMemoryItem(id, text, body.Category, body.TogglePin)
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "memories": items})
	})

	mux.HandleFunc("DELETE /api/memory/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		deleteMemoryItem(id)
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "memories": loadMemories()})
	})

	mux.HandleFunc("POST /api/tools/test", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string         `json:"name"`
			Args map[string]any `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		res := executeLocalTool(body.Name, body.Args)
		delete(res, "_native_screen_b64")
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "result": res})
	})

	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": listSessionSummaries()})
	})

	mux.HandleFunc("GET /api/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec, err := getSessionRecord(r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return
		}
		writeJSON(w, http.StatusOK, rec)
	})

	mux.HandleFunc("GET /api/sessions/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		cleanID := filepath.Base(r.PathValue("id"))
		format := strings.ToLower(r.URL.Query().Get("format"))
		if format == "md" {
			data, err := os.ReadFile(filepath.Join(sessionsDir, cleanID+".md"))
			if err != nil {
				http.Error(w, "Markdown transcript not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"session_%s.md\"", cleanID))
			_, _ = w.Write(data)
			return
		}
		data, err := os.ReadFile(filepath.Join(sessionsDir, cleanID+".json"))
		if err != nil {
			http.Error(w, "JSON transcript not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"session_%s.json\"", cleanID))
		_, _ = w.Write(data)
	})

	mux.HandleFunc("DELETE /api/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		deleteSessionRecord(r.PathValue("id"))
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "sessions": listSessionSummaries()})
	})

	mux.HandleFunc("GET /api/notes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"notes": listNotes()})
	})

	mux.HandleFunc("POST /api/keys/test/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		envKeys := getAllEnvKeys()
		val := envKeys[name]
		if val == "" {
			writeJSON(w, http.StatusNotFound, map[string]any{"status": "error", "message": "Key not found"})
			return
		}
		ok, latencyMs, msg := verifyAPIKey(val)
		if ok {
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "latency_ms": latencyMs, "message": msg})
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"status": "error", "message": msg})
		}
	})
}

func verifyAPIKey(apiKey string) (bool, int64, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return false, 0, err.Error()
	}

	resp, err := client.Models.GenerateContent(ctx, "gemini-3.8-flash", []*genai.Content{
		{
			Role:  "user",
			Parts: []*genai.Part{{Text: "Reply with OK"}},
		},
	}, nil)
	if err != nil {
		return false, 0, err.Error()
	}
	latency := time.Since(start).Milliseconds()
	return true, latency, strings.TrimSpace(resp.Text())
}
