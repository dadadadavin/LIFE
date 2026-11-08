# Comprehensive Context Management Architecture in Lifel

This document provides a complete, granular, and transparent guide to how **Context**, **System Prompts**, **Token Lifecycles**, and **Long-Term Memory** operate in Lifel when communicating with Google's Gemini Multimodal Live API.

---

## 1. What is "Context" in Gemini Multimodal Live?

In a standard REST API call (e.g. `generateContent`), context is a static array of messages sent with every HTTP request. 

In the **Gemini Multimodal Live API** (`gemini-3.8-live` over WebSocket), context is fundamentally different:
1. **Stateful In-Memory Session**: Once the connection opens, Google's TPU cluster maintains an active **KV-cache (Key-Value attention cache)** for your stream.
2. **Streaming Token Ingestion**: Every millisecond of 16kHz audio, every 1080p camera frame, and every tool execution continuously appends tokens to this server-side cache.
3. **Bimodal State Tracking**: The context contains both **raw sensory tokens** (audio waveforms, JPEG visual tokens) and **semantic tokens** (system instructions, function schemas, and transcript text).

---

## 2. Anatomy of the Initial Handshake Payload ($t = 0$)

When you click **Connect**, Go's backend builds the initial connection config (`genai.LiveConnectConfig`). Before any microphone audio is streamed, Google receives two primary blocks that form the **Base Context**:
1. **The System Instruction** ($\approx 600\text{--}1,200\text{ tokens}$)
2. **The 12 Tool Function Declarations** ($\approx 1,200\text{ tokens}$)

### A. The 6 Layers of the System Prompt

The System Instruction is assembled dynamically by `buildFullSystemPrompt()` in `config.go`. It consists of 6 contiguous sections:

```
┌────────────────────────────────────────────────────────────────────────┐
│ Layer 1: Base Persona & Core Rules (User customizable in config)       │
├────────────────────────────────────────────────────────────────────────┤
│ Layer 2: Response Style Directive ([RESPONSE STYLE: CONCISE], etc.)    │
├────────────────────────────────────────────────────────────────────────┤
│ Layer 3: Runtime Self-Awareness Block (OS, Time, Model, Tools, Keys)   │
├────────────────────────────────────────────────────────────────────────┤
│ Layer 4: Persistent Memory Bank (Injected from memory.json)            │
├────────────────────────────────────────────────────────────────────────┤
│ Layer 5: Resumed Session Context (Injected if continuing a session)    │
├────────────────────────────────────────────────────────────────────────┤
│ Layer 6: Dynamic Tool Directives (Guidance for execution)              │
└────────────────────────────────────────────────────────────────────────┘
```

#### Verbatim Example of the Exact String Sent to Google:

```text
You are Lifel, a fast, intelligent, and natural real-time voice and vision assistant running natively in Go on the user's Mac. Always respond in the same language the user speaks (English or Indonesian). Keep your spoken answers natural, direct, and easy to listen to. When the user shares their webcam or screen, inspect text, objects, code, or hardware accurately. Use your available tools proactively whenever asked to search the web, read articles, check Mac status, use the clipboard, capture the screen, or save memories.

[RESPONSE STYLE: CONCISE] Keep spoken answers brief (1 to 2 sentences by default) unless the user asks for a deep explanation.

=== RUNTIME SELF-AWARENESS & SYSTEM METADATA ===
- Assistant Name: Lifel
- Exact Base AI Model: gemini-3.8-live (Google Gemini 3.8 Live multimodal bidirectional model)
- Active Voice Profile: Charon
- Active API Key Slot: GEMINI_API_KEY_5
- Backend Engine: Go (go1.24.0 darwin/arm64) + google.golang.org/genai SDK + HTMX + Tailwind CSS
- Local Date & Time: 2026-11-08 20:20:00 (Sunday)
- Active Speech Languages (Bilingual STT): en-US, id-ID (Transcription Mode: SMART)
- Custom Vocabulary & Domain Terms: Lifel, Gemini 3.8 Live, Samsung Innovation Campus, Indonesia, MacBook Air, Golang, HTMX, Tailwind CSS, KiCad, ESP32
- Web Search & Intelligence Engine: tavily (Depth: advanced)
- Enabled Local Tools (13): get_mac_system_info, web_search, read_webpage, crawl_website, save_user_memory, search_user_memory, list_project_files, read_project_file, write_project_note, calculate_math, mac_clipboard, open_url_or_app, capture_mac_screen
- Note: If the user asks what model you are, what voice you use, or how you are built, state these exact facts directly.
================================================

=== PERSISTENT USER MEMORY BANK ===
- [PINNED] [SUMMARY] Last session (20261106_185935): User verified Robotics playground decoupling and memory resets.
- [PINNED] [PREFERENCE] User prefers direct, concise, practical answers with zero AI slop.
- [PINNED] [USER] User works on macOS (Apple Silicon) in the local project workspace.
===================================

=== RESUMED CONVERSATION CONTEXT (Session 20261106_185935 from 2026-11-06T18:59:35Z) ===
The user is continuing this previous conversation. Pick up naturally where you left off:
[18:59:36] USER: Hello. Are you there?
[18:59:37] MODEL: Yes, I'm here! What can I help you with today?
[18:59:40] USER: Can you check my battery status?
[18:59:42] MODEL: Your MacBook Air is at 84% battery and currently discharging.
=== END RESUMED CONTEXT ===
```

---

### B. Tool Schemas in Context

Google also receives JSON Schemas for each of the enabled tools. For example, `web_search` and `get_mac_system_info` are declared as:

```json
[
  {
    "name": "web_search",
    "description": "Search the live web (Tavily AI, DuckDuckGo, Google News) for current events or technical facts.",
    "parameters": {
      "type": "OBJECT",
      "properties": {
        "query": { "type": "STRING", "description": "The search query to look up on the web." }
      },
      "required": ["query"]
    }
  },
  {
    "name": "get_mac_system_info",
    "description": "Get real-time Mac battery percentage, power source, CPU load, free disk, and macOS version.",
    "parameters": {
      "type": "OBJECT",
      "properties": {}
    }
  }
]
```

### Initial Token Budget at Handshake ($t = 0$)
* **System Prompt**: $\sim 850\text{ tokens}$
* **13 Tool Function Schemas**: $\sim 1,250\text{ tokens}$
* **Initial KV-Cache Base**: $\approx \mathbf{2,100\text{ tokens}}$ before you say a single word.

---

## 3. In-Flight Context Dynamics ($t > 0$)

Once connected, context is **cumulative**. Every action adds tokens:

| Action | Token Cost | Rate / Frequency |
| :--- | :--- | :--- |
| **User Speech (Mic)** | $\sim 25\text{--}35\text{ tokens/second}$ | Converted from 16kHz PCM audio stream |
| **Vision (Webcam / Screen)** | $\sim 258\text{ tokens/frame}$ | Ingested at 1 frame per second (if camera active) |
| **Gemini Spoken Response** | $\sim 30\text{ tokens/second}$ | Converted from 24kHz output audio stream |
| **Tool Execution Roundtrip** | $\sim 100\text{--}600\text{ tokens}$ | Tool call arguments + Tool response JSON |

---

### Walkthrough of Context Growth Over 3 Turns

```
  Turn 0: Connection Handshake
  ┌────────────────────────────────────────────────────────┐
  │ Base Context: System Prompt + Tool Schemas             │  --> 2,100 Tokens
  └────────────────────────────────────────────────────────┘
                             │
                             ▼
  Turn 1: User asks "What is my battery level?" (3 seconds of audio)
  ┌────────────────────────────────────────────────────────┐
  │ Base Context (2,100)                                   │
  │ + User Audio Input Chunks (~90 tokens)                 │
  │ + Interim & Final STT Transcription                    │
  │ + Model ToolCall: get_mac_system_info() (~40 tokens)   │
  │ + Local Tool Response JSON (~85 tokens)                │
  │ + Model Spoken Audio Response (~110 tokens)            │  --> 2,425 Tokens
  └────────────────────────────────────────────────────────┘
                             │
                             ▼
  Turn 2: User says "Check news on Apple M5 chips" + Screen Shared
  ┌────────────────────────────────────────────────────────┐
  │ Previous Context (2,425)                               │
  │ + 1 Screen Capture JPEG Frame (~258 tokens)            │
  │ + User Audio (~120 tokens)                             │
  │ + ToolCall: web_search({"query":"Apple M5 chip news"}) │
  │ + Tavily Search Result JSON (~550 tokens)              │
  │ + Model Spoken Response (~160 tokens)                  │  --> 3,553 Tokens
  └────────────────────────────────────────────────────────┘
```

Notice that **every previous turn remains in Google's GPU KV-cache**, allowing the assistant to resolve pronouns (*"tell me more about that"*, *"open that link"*) naturally.

---

## 4. The Server-Side Sliding Window (Long Sessions)

If a voice conversation lasts for 30 minutes, or you share video for 10 minutes, context can approach $100,000+$ tokens. Unmanaged, this would cause latency spikes and out-of-memory errors.

### The Compaction Mechanism
In `live_ws.go`, we instruct Google to apply a **Sliding Window Compression**:
* **Trigger Tokens**: `80,000`
* **Target Tokens**: `40,000`

```
  0 Tokens ────────────────────────────────────────► 80,000 Tokens (TRIGGER HIT)
                                                            │
  ┌─────────────────────────────────────────────────────────▼────────────────┐
  │ Google's Server Compaction Logic:                                        │
  │                                                                          │
  │  [System Instruction & Tools]  ======> PROTECTED (Never Evicted)         │
  │  [Turns 1 through 45]          ======> EVICTED (Oldest Audio & Tool Dumps)│
  │  [Turns 46 through 60]         ======> RETAINED (Recent Turn History)    │
  └─────────────────────────────────────────────────────────┬────────────────┘
                                                            │
                                                            ▼
                                                     40,000 Tokens (New Baseline)
```

1. When total tokens reach **80,000**, Google evicts the oldest conversation audio chunks.
2. The initial **System Prompt**, **Memory Bank**, and **Tool Schemas** are **permanently anchored** and never evicted.
3. The most recent turns are retained, bringing the context back down to **40,000 tokens**.
4. Latency remains at sub-100ms.

---

## 5. Resumption Architecture: How Past Context Survives

What happens when you hang up, your Wi-Fi glitches, or you restart the application?

### Tier A: Warm Resumption (Google Server KV-Cache)
* While connected, Google periodically returns `resp.SessionResumptionUpdate{NewHandle: "uuid"}`.
* Lifel immediately saves this UUID to `config.json` (`last_resumption_handle`).
* If you disconnect and reconnect within **5–15 minutes**, Lifel passes:
  ```go
  SessionResumption: &genai.SessionResumptionConfig{Handle: lastHandle}
  ```
* Google matches the handle to the still-warm TPU memory. 
* **Token Cost to Reconnect**: **0 extra tokens**. The session resumes instantly with exact neural fidelity.

### Tier B: Cold Resumption (Disk Transcript Injection)
* After $\sim 15$ minutes of inactivity, Google drops the warm KV-cache from TPU memory. Any attempt to use the handle returns a `Resumption handle expired` error.
* In `live_ws.go`, Lifel catches this failure and falls back to **Layer 2 (Cold Resumption)**:
  1. Opens `sessions/<id>.json` on your Mac.
  2. Reads the conversation turns.
  3. Slices the last **20 turns**.
  4. Injects them under `=== RESUMED CONVERSATION CONTEXT ===` into the system prompt.
* **Token Cost**: Consumes $\sim 1,500\text{--}2,500$ input tokens, but guarantees you can pick up a conversation from **days or weeks ago**.

---

## 6. The Autonomous Memory Distillation Loop (`memory.json`)

Transcripts in `sessions/` are raw and verbose. To maintain durable facts across all future sessions without blowing up prompt tokens, Lifel uses a **Background Distillation Worker**.

### When Does It Run?
Immediately upon WebSocket disconnect (if session turns $\ge 2$).

### The Distillation Pipeline
Go launches a background goroutine that dispatches the raw transcript to `gemini-3.5-flash-lite` (with cascading fallback to `3.6-flash` and `3.7-flash`):

#### 1. Exact Extraction Prompt:
```text
Analyze this voice conversation transcript between the User and Gemini.
Return a JSON object with two fields:
1. "summary": A concise 1-2 sentence summary of what was discussed or accomplished in this session.
2. "facts": A JSON array of new, durable personal facts, preferences, or project details about the User worth remembering long-term (each with "fact" and "category" in ["preference", "project", "user", "task"]). If no new durable facts were shared, return [].

Transcript:
User: I am working on the Lifel codebase in Go and HTMX.
Gemini: Got it! What part are you working on?
User: The context management docs. Also make sure to remember that I prefer concise explanations.
```

#### 2. Exact Model Response Received:
```json
{
  "summary": "User worked on Lifel context management documentation in Go and HTMX.",
  "facts": [
    {
      "fact": "Working on Lifel codebase using Go and HTMX",
      "category": "project"
    },
    {
      "fact": "Prefers concise explanations",
      "category": "preference"
    }
  ]
}
```

#### 3. Merge Logic into `memory.json`:
* **Rolling Summary**: Replaces the single previous `category: "summary"` item. There is only ever **one** rolling summary in `memory.json`.
* **Durable Facts**: Deduplicates against existing entries and appends new items.
* **Result**: On your *next* connection, these facts appear in `=== PERSISTENT USER MEMORY BANK ===`, consuming under 300 tokens while keeping the AI fully informed.

---

## 7. Context Observability & Diagnostics

You can verify and monitor context consumption in real time:

1. **In the Terminal Logs**:
   ```text
   20:20:15 [GEMINI SUCCESS] Live session ready! Model: gemini-3.8-live | Voice: Charon | Key: GEMINI_API_KEY_5
   20:20:15 [RESUMPTION] Handle saved: 9ac476cb-20ab-4ce0-b064-733ff9e842be
   20:20:22 [USER] Check my system info.
   20:20:24 [TOOL CALL] get_mac_system_info({})
   20:20:24 [LATENCY] Turn latency: 84ms | Tokens: prompt=2140, resp=42, total=2182
   ```
2. **In the UI (Header & Sessions Tab)**:
   * The **Session History** tab displays the exact `Total Tokens` consumed for every past session.
   * Clicking any session lets you inspect the exact raw transcript that was archived.
3. **In the Live Token Metadata**:
   * `PromptTokens`: Current baseline + history.
   * `ResponseTokens`: Words spoken by the AI.
   * `TotalTokens`: Total memory footprint in the active session.

---

## 8. Summary: How All Context Systems Interact

| Context Layer | Location | Lifetime | Token Size | Purpose |
| :--- | :--- | :--- | :--- | :--- |
| **System Instruction** | `config.go` | Rebuilt per connection | $\sim 850$ tokens | Identity, rules, self-awareness, active voice |
| **Tool Schemas** | `tools.go` | Static per connection | $\sim 1,250$ tokens | Definitions for the 13 local tools |
| **Active Turn KV-Cache** | Google Server RAM | Duration of call | $0 \to 80,000$ tokens | Natural dialogue, pronouns, audio/vision perception |
| **Sliding Window** | Google Server | Automatic trigger at 80k | Compresses to 40k | Prevents lag and OOM on long calls |
| **Resumption Handle** | `config.json` | 5–15 minutes | 0 extra tokens | Instant zero-cost reconnection on network drops |
| **Session Transcripts** | `sessions/*.json` | Permanent on disk | Full transcript | Cold resumption fallback, history inspection |
| **Memory Bank** | `memory.json` | Permanent on disk | $\sim 200\text{--}500$ tokens | Distilled facts & rolling summary injected into every prompt |
