# Comprehensive Context Management Architecture in Lifel

This document specifies the complete, authoritative architecture of **Context Management**, **KV-Cache Lifecycles**, **System Instruction Assembly**, **Deep Local Semantic RAG**, **Session Resumption**, **Tool Response Scheduling**, and **Autonomous Memory Distillation** in Lifel. It reflects the production implementation powered by Google's **Gemini Multimodal Live API (`BidiGenerateContent`)** via the `google.golang.org/genai` Go SDK.

---

## 1. Executive Overview & The Jarvis Architecture

Lifel is engineered as an **omnipresent, personal AI operating assistant for macOS** inspired by the Jarvis archetype:
* **Single High-Speed Loop**: Lifel operates as a single, ultra-responsive multimodal loop. By avoiding bloated sub-agent orchestration trees, Lifel minimizes turn latency to sub-100ms, avoids context multiplication, and delivers immediate, conversational voice-and-vision pairing.
* **Dual-Engine Operation**:
  1. **Real-Time Multimodal Live Engine (`gemini-3.8-live`)**: Handles continuous bidirectional 16kHz audio, 1080p webcam camera vision frames, native macOS tool executions, and sub-100ms conversational audio output over a stateful WebSocket.
  2. **Autonomous Local RAG & Memory CRUD Controller (`gemini-embedding-2` + `gemini-3.8-flash` cascade)**: Operates non-blockingly out-of-band via deterministic mid-call checkpoints and post-call passes. It embeds knowledge with 3,072-dimensional normalized vectors, resolves contradictions via an atomic CRUD loop (`INSERT`, `MERGE`, `UPDATE`, `DELETE`), and keeps system prompt token baselines minimal without imposing token or latency overhead on live calls.

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                                 LIFEL ARCHITECTURE                                     │
├────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                        │
│   ┌────────────────────────────────────────────────────────────────────────────────┐   │
│   │                      MULTIMODAL LIVE LOOP (WebSocket)                          │   │
│   │                                                                                │   │
│   │   User Mic (16kHz PCM) ────┐                                                   │   │
│   │   Webcam Camera (1-2fps) ──┼──► [Go Server: live_ws.go] ──► Gemini 3.8 Live    │   │
│   │   Mac System Tools (11)   ──┘             │                         │          │   │
│   │                                           │                         ▼          │   │
│   │   Browser Speaker (24kHz)  ◄──────────────┴─────────────── Spoken Audio / TTS │   │
│   └────────────────────────────────────────────────────────────────────────────────┘   │
│                                           │ (Deterministic Triggers A & B)             │
│                                           ▼                                            │
│   ┌────────────────────────────────────────────────────────────────────────────────┐   │
│   │                  DEEP LOCAL SEMANTIC RAG & MEMORY CRUD LOOP                    │   │
│   │                                                                                │   │
│   │   Unindexed Turns Batch ──► [gemini-embedding-2 (3072-dim)] ──► Top-10 Chunks   │   │
│   │                                                                    │           │   │
│   │                                                                    ▼           │   │
│   │   Flash Controller (3.8-flash) ◄── Existing Chunks + Batch Transcript          │   │
│   │            │                                                                   │   │
│   │            ▼                                                                   │   │
│   │   Atomic Mutations: INSERT | MERGE | UPDATE | DELETE                           │   │
│   │            │                                                                   │   │
│   │            ▼                                                                   │   │
│   │   memory_rag.json (Disk-backed Vector Store, 500-1500 tokens/chunk)            │   │
│   │   + memory.json (Rolling Session Summary & Pinned Identity Bank)               │   │
│   └────────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                        │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Gemini Live Multimodal Context: The TPU KV-Cache

In traditional REST APIs (e.g. `generateContent`), the client transmits the entire conversation history on every request, paying quadratic attention and serialization penalties.

In the **Gemini Multimodal Live API (`BidiGenerateContent`)**, context is an **active, server-side Key-Value (KV) attention cache**:
1. **Stateful In-Memory Session**: Once the connection opens via `client.Live.Connect()`, Google's TPU cluster allocates a dedicated KV-cache for the connection.
2. **Streaming Token Ingestion**: Every millisecond of 16kHz audio, every 1080p webcam camera frame, and every tool result appends directly to this server-side cache.
3. **Multimodal Cross-Attention**: Audio waveforms and visual embeddings reside in the same attention space, enabling Gemini to ground spoken pronouns (*"What is that chip on the breadboard?"*) in the webcam video frame streamed 500ms earlier.

### Live Token Rates on the Wire
| Stream Channel | Token Generation Rate | Engineering Implication |
| :--- | :--- | :--- |
| **User Mic (16kHz PCM)** | $\sim 25\text{--}35\text{ tokens/sec}$ | Ingested via `SendRealtimeInput` as raw PCM chunks. |
| **Vision (Webcam Camera)** | $\sim 258\text{ tokens/frame}$ | Ingested at 1.0–2.0 FPS as 1080p JPEG blobs. |
| **Gemini Spoken Voice** | $\sim 30\text{ tokens/sec}$ | Converted directly to 24kHz PCM audio output. |
| **Native Tool Execution** | $\sim 80\text{--}500\text{ tokens/call}$ | Call arguments + JSON response payload. |

---

## 3. Vision Pipeline Simplification & Local RAG Paradigm Shift

### Desktop Screen Capture Removal
* **The Problem**: Passive desktop screen-recording and on-demand desktop display captures (`screencapture -x`) consumed massive token volumes ($\sim 15,480\text{ tokens/min}$ at 1 FPS), triggering 80,000-token server-side KV-cache evictions in under 5 minutes and blowing out conversation history. Furthermore, desktop screen frames frequently leaked sensitive desktop elements, private messages, and API keys.
* **The Production Architecture**: All desktop screen-recording code—including `capture_mac_screen`, `_native_screen_b64`, `_request_browser_hd_frame`, and passive screen-sharing handlers—is **completely stripped** from `tools.go`, `config.go`, `handlers.go`, and `live_ws.go`.
* **Retained Vision Pipeline**: Lifel retains exclusively the clean **16kHz PCM microphone stream** and the **1–2 FPS webcam camera pipeline** (`SendRealtimeInput` video blobs), delivering crisp visual grounding for hardware inspection, whiteboards, paper schematics, and physical environments without desktop token bloat.

### The Local RAG Engine (`memory_rag.go`)
Instead of cramming over-compressed one-liners into `SystemInstruction`—which starves the model of nuance and triggers hallucinations—Lifel replaces the shallow 25-item list with a **Deep Local Semantic RAG Engine**:
* **Embedding Model**: **`gemini-embedding-2`** (`client.Models.EmbedContent`), generating normalized **3,072-dimensional vectors** across an **8,192-token input window** (with automated failover to `text-embedding-004`).
* **Chunk Sizing**: Stores rich, multi-paragraph episodic, technical, and project knowledge blocks (**500–1,500 tokens per chunk**) inside `memory_rag.json`.
* **Hybrid Relevance Scoring**: When querying the local RAG bank, chunks are ranked using a hybrid scoring algorithm combining vector cosine similarity with an exponential recency decay:
  $$\text{Score} = 0.85 \times \text{CosineSimilarity} + 0.15 \times \text{RecencyWeight}$$
  Where $\text{RecencyWeight}$ applies a **7-day half-life**:
  $$\text{RecencyWeight} = 0.5^{(\Delta t / 7\text{ days})} = \exp\left(-\ln(2) \times \frac{\Delta t}{7 \times 86400\text{s}}\right)$$
  Brand new memories score $1.0$ on recency; memories 7 days old score $0.5$; memories 14 days old score $0.25$.

---

## 4. Official Gemini Live API Specifications vs. Lifel Architecture

To maintain a production-grade harness, Lifel strictly adheres to the official wire protocol of `google.golang.org/genai`:

| Capability / Concept | Common Misconception | Actual Gemini Live API Wire Behavior | Lifel Production Implementation (`live_ws.go`, `memory_rag.go`) |
| :--- | :--- | :--- | :--- |
| **Warm Resumption TTL** | "Resumption handles expire in 5–15 minutes." | **Socket lifetime is $\sim 10$ minutes**, but **Resumption Handles are valid for 2 hours** after session termination. | Resumes via `SessionResumptionConfig{Handle: lastHandle}` up to 2 hours after disconnection at 0 extra tokens. |
| **Resumption Update Handling** | "Always save `NewHandle` whenever update arrives." | `SessionResumptionUpdate` includes `Resumable bool` and `NewHandle string`. `NewHandle` is **empty** when `Resumable == false`. | Checks `update.Resumable && update.NewHandle != ""` before saving to avoid overwriting valid handles with `""`. |
| **Long Session Continuity** | "30-minute calls stay on 1 WebSocket." | Single WebSocket connections drop at $\sim 10$ minutes with an `ABORTED` code. Server sends `GoAway` first. | Listens for `resp.GoAway != nil` with `TimeLeft`, broadcasts warning, and prepares warm auto-rotation. |
| **Cold Session Resumption** | "Inject 20 old turns into System Instruction." | `SystemInstruction` is **permanently protected from Sliding Window eviction**, making old turns uncompressible. | Seeds historical turns into the mutable KV-cache via `session.SendClientContent` with `TurnComplete: false`. |
| **Bilingual Speech-to-Text** | "STT is configured via text in System Prompt." | `gemini-3.8-live` processes audio natively; transcripts are enabled via `InputAudioTranscription` configs. | Configured via `LiveConnectConfig.RealtimeInputConfig` and `OutputAudioTranscription`. |
| **Tool Response Scheduling** | "Gemini always pauses until tools return." | Live API supports `WHEN_IDLE`, `INTERRUPT`, and `SILENT` scheduling. | Uses `WHEN_IDLE` for `memory(action="recall")` and `SILENT` for asynchronous mutate actions (`remember`, `update`, `forget`). |
| **Interrupted Tool Calls** | "Tools always execute to completion." | When a user barges in mid-tool call, Google sends `LiveServerToolCallCancellation` with cancelled IDs. | Listens for `resp.ToolCallCancellation`, logs cancelled IDs, and suppresses stale responses. |

---

## 5. Handshake Payload Anatomy ($t = 0$)

When you click **Connect**, Lifel constructs the initial `genai.LiveConnectConfig`. Before any microphone audio is streamed, Google receives two primary blocks that form the **Base Context**:
1. **The System Instruction** ($\approx 550\text{--}650\text{ tokens}$)
2. **The 11 Native Tool Function Declarations** ($\approx 1,050\text{ tokens}$)

```
Initial Context Baseline: ~1,600 - 1,700 Tokens (Immutable Anchor)
```

### The 4-Layer System Prompt Architecture (`config.go`)

The System Instruction is assembled dynamically by `buildFullSystemPrompt()` into 4 clean, focused layers:

```
┌────────────────────────────────────────────────────────────────────────────────┐
│ Layer 1: Jarvis Persona & Core Grounding (Proactive RAG retrieval instruction) │
├────────────────────────────────────────────────────────────────────────────────┤
│ Layer 2: Spoken Response Style Directive ([RESPONSE STYLE: CONCISE], etc.)    │
├────────────────────────────────────────────────────────────────────────────────┤
│ Layer 3: Runtime Operating Context (Clean macOS, Time, Proactive Memory Tool) │
├────────────────────────────────────────────────────────────────────────────────┤
│ Layer 4: Persistent Core Identity & Project Index (STRICTLY UNDER 400 TOKENS)  │
└────────────────────────────────────────────────────────────────────────────────┘
```

#### Layer 4 Strict Token Budgeting (< 400 Tokens)
Layer 4 is populated by `getPinnedAndProjectIndex()` and contains:
1. `[PINNED]` Core user identity facts (max 5 items, e.g. user OS, hardware setup, core preference).
2. An index of active project names (e.g. `Lifel Assistant`, `ESP32 Perception`).
3. An explicit grounding directive informing the model that deep episodic, architectural, and technical details reside in the local RAG vector store, requiring proactive calls to `memory(action="recall", query="...")`.

#### Verbatim Example of System Instruction Sent to Google:

```text
You are Lifel, an advanced Jarvis-like personal AI operating assistant running natively on macOS. Always respond in the same language the user speaks (English or Indonesian). Keep spoken responses crisp, natural, direct, and intelligent with zero conversational fluff. You possess deep episodic, technical, and project knowledge stored in a local semantic RAG engine. Proactively invoke the 'memory' tool with action='recall' whenever the conversation touches on past projects, hardware setups, user preferences, or multi-step tasks to retrieve full context before answering. Execute native Mac tools smoothly and proactively.

[RESPONSE STYLE: CONCISE] Keep spoken answers brief (1 to 2 sentences by default) unless the user asks for a deep explanation.

=== RUNTIME OPERATING CONTEXT ===
- Assistant Name: Lifel (Omnipresent Personal Operating Assistant)
- Platform & Hardware: macOS Apple Silicon (darwin/arm64)
- Local Date & Time: 2026-11-08 21:00:00 (Sunday)
- Primary Location & Timezone: Asia/Jakarta (WIB) / User Local Time
- Active Voice Profile: Charon
- Custom Vocabulary & Domain Terms: Lifel, Gemini 3.8 Live, Samsung Innovation Campus, Indonesia, MacBook Air, Golang, HTMX, Tailwind CSS, KiCad, ESP32
- Enabled Native Tools (11): get_mac_system_info, web_search, read_webpage, crawl_website, memory, list_project_files, read_project_file, write_project_note, calculate_math, mac_clipboard, open_url_or_app
- Proactive Memory Directive: Detailed episodic history, architectural decisions, and technical specifications are stored in your local RAG index. Proactively call memory(action='recall', query='...') whenever the user mentions past discussions, project context, hardware details, or user preferences.
- Operating Directive: Act like Jarvis—proactive, razor-sharp, helpful, and concise. Never leak internal API keys, slot names, or low-level web framework details.
================================

=== PERSISTENT CORE IDENTITY & ACTIVE PROJECTS ===
- [PINNED] [USER] User works on macOS Apple Silicon in the local workspace.
- [PINNED] [PREFERENCE] User prefers direct, crisp, natural answers with zero AI slop.
- Active Projects: ESP32 Hardware Lab, Lifel macOS Agent
Note: Deep context, episodic history, and technical details reside in RAG. Use memory(action='recall') for full context.
==================================================
```

---

## 6. Deterministic Mid-Call & Post-Call Summarization Timing

To guarantee that **zero conversational detail is ever lost** when Google's server-side sliding window compacts the TPU KV-cache, Lifel implements a deterministic, two-trigger indexing pipeline in `live_ws.go`:

```
         Turn Ingestion Stream (User, Model, Tool Results)
                               │
            ┌──────────────────┴──────────────────┐
            ▼                                     ▼
   [Trigger A: Mid-Call]                 [Trigger B: Call End]
   - Every 16 unindexed turns            - Fires on WebSocket close
   - OR TotalTokens >= 55,000            - Ignores GoAway rotations
            │                                     │
            └──────────────────┬──────────────────┘
                               ▼
        Slice: turnsLog[lastIndexedIdx : currentIdx]
        Advance: lastIndexedIdx = currentIdx
                               │
                               ▼
        go consolidateTranscriptBatch(sessionID, batch)
        (Runs in background at sub-100ms live latency)
```

### Trigger A: Pre-Compaction Mid-Call Checkpoint
* **Condition**: Evaluated whenever new turns are finalized in `turnsLog` or when `resp.UsageMetadata.TotalTokenCount` updates. If `len(turnsLog) - lastIndexedTurnIdx >= 16` **OR** `TotalTokenCount >= 55,000` (well ahead of Google's 80,000-token eviction threshold).
* **Execution**: Lifel atomically slices the unindexed turns `turnsLog[lastIndexedTurnIdx:]`, advances `lastIndexedTurnIdx`, and dispatches the batch to `go consolidateTranscriptBatch()`.
* **Zero Latency Impact**: The background worker runs in an independent goroutine; the live WebSocket continues processing 16kHz audio at sub-100ms response time.

### Trigger B: True Call Termination Pass
* **Condition**: Fires when the WebSocket connection closes and `!sawGoAway` is confirmed (ignoring 10-minute automated `GoAway` socket rotations).
* **Execution**: Any remaining unindexed turns (`turnsLog[lastIndexedTurnIdx:]`) are immediately packaged and dispatched to `go consolidateTranscriptBatch()`, ensuring every final decision, action item, and user preference is embedded and persisted to disk.

---

## 7. The Stateful Memory CRUD Loop (`INSERT`, `MERGE`, `UPDATE`, `DELETE`)

Lifel upgrades background memory consolidation from a dumb append-only summarizer into an autonomous **Knowledge Graph CRUD Controller** driven by **`gemini-3.8-flash`** (with `ResponseMIMEType: "application/json"` and a strict JSON schema):

```
       New Batch Transcript (from Trigger A or B)
                           │
                           ▼
     Embed Batch with gemini-embedding-2 (3,072 dimensions)
                           │
                           ▼
     Query Local RAG Store (memory_rag.json) for Top-10 Matches
                           │
                           ▼
  Construct Prompt for Gemini 3.8 Flash:
  - Existing Candidate Chunks (IDs, Titles, Categories, Contents)
  - New Conversation Transcript Batch
                           │
                           ▼
  Flash outputs JSON with Atomic Operations:
  ┌───────────────────────────────────────────────────────────────┐
  │ [INSERT] => Add new rich knowledge block (500-1500 tokens)    │
  │ [UPDATE] => Amend existing chunk by specific target_id        │
  │ [MERGE]  => Combine into target_id and prune secondary_ids   │
  │ [DELETE] => Purge obsolete/contradicted chunk by target_id    │
  └───────────────────────────────────────────────────────────────┘
                           │
                           ▼
  Execute Mutations -> Re-Embed with gemini-embedding-2 -> Save
```

### JSON Schema & Atomic Operations
```json
{
  "summary": "1-2 sentence rolling summary of the conversation batch...",
  "operations": [
    {
      "op": "INSERT | UPDATE | MERGE | DELETE",
      "target_id": "UUID (required for UPDATE, MERGE, DELETE)",
      "secondary_ids": ["UUIDs to delete for MERGE"],
      "category": "episodic | technical | preference | project",
      "title": "Descriptive Title",
      "content": "Rich, multi-paragraph factual knowledge (500-1500 tokens)..."
    }
  ]
}
```

* **`INSERT`**: Creates a new `MemoryChunk`, computes its normalized 3,072-dimensional vector using `gemini-embedding-2`, and appends it to `memory_rag.json`.
* **`UPDATE`**: Locates the chunk matching `target_id`, updates its title, category, and content, updates `UpdatedAt = time.Now()`, and re-embeds the text.
* **`MERGE`**: Consolidates new and existing details into `target_id`, deletes redundant `secondary_ids` to prevent fragment bloat, and re-embeds.
* **`DELETE`**: Purges obsolete, incorrect, or contradicted knowledge blocks from `memory_rag.json`.
* **Rolling Summary**: Updates the session summary in `memory.json` using `addMemoryItem` for web UI visibility.

All mutations are guarded by `ragMu sync.RWMutex` with atomic temp-file renaming.

---

## 8. Unified `memory` Tool Interface & Asynchronous Execution

Lifel consolidates `save_user_memory` and `search_user_memory` into a single, high-leverage **`memory`** tool:

```go
{
    Name: "memory",
    Title: "Unified Long-Term Memory & Local RAG",
    Description: "Deep semantic search and stateful management of episodic, technical, and preference memories...",
    Category: "memory",
    Schema: {
        Properties: {
            "action":  {"type": "STRING", "enum": ["recall", "remember", "update", "forget"]},
            "query":   {"type": "STRING", "description": "Search query for semantic retrieval (recall)"},
            "content": {"type": "STRING", "description": "Rich factual detail (remember, update)"},
            "id":      {"type": "STRING", "description": "Chunk UUID (update, forget)"}
        },
        Required: ["action"]
    }
}
```

### Tool Execution & Scheduling Matrix

| Action | Execution Mode | Scheduling Mode | Behavior |
| :--- | :--- | :--- | :--- |
| **`recall`** | Synchronous ($\sim 100\text{--}200\text{ms}$) | **`WHEN_IDLE`** | Embeds `query` with `gemini-embedding-2`, calculates hybrid similarity + recency scores, and returns top 3 matching chunks formatted in markdown. |
| **`remember`** | Background Goroutine | **`SILENT`** | Immediately returns `{"status": "queued", ...}` to the live session with `FunctionResponseSchedulingSilent`. Embedding and disk writing run asynchronously so the voice model never stutters. |
| **`update`** | Background Goroutine | **`SILENT`** | Queues async re-embedding and chunk mutation, returning silently to avoid speech interruption. |
| **`forget`** | Background Goroutine | **`SILENT`** | Queues async chunk removal, returning silently to live session. |

---

## 9. Real-Time Concurrency, Ordering & Execution Invariants (The 7 Runtime Guarantees)

Production full-duplex conversational systems must handle subtle race conditions that arise when audio chunks, vision frames, tool executions, and WebSocket stream updates intersect concurrently. In `live_ws.go`, Lifel enforces seven strict runtime guarantees:

### 9.1 Chronological Turn Ordering (`flushPendingUserTurn`)
* **The Invariant**: Lifel defines `flushPendingUserTurn()`, invoked immediately at the entry of `resp.ToolCall`, `sc.OutputTranscription`, and `sc.ModelTurn`. This guarantees that user speech is committed to `turnsLog` **prior to tool invocation**: `[USER] -> [TOOL CALL] -> [TOOL RESULT] -> [MODEL]`.

### 9.2 Non-Blocking Asynchronous Tool Dispatching
* **The Invariant**: All `resp.ToolCall` instances are dispatched in background worker goroutines (`go func(tc *genai.LiveServerToolCall)`). The receive loop continues polling without delay. A thread-safe `cancelledTools map[string]bool` tracks server cancellations, and tool responses are suppressed both before and after execution if a cancellation frame arrived.

### 9.3 Thread-Safe Session Mutation Wrappers (`sessionWriteMu`)
* **The Invariant**: Lifel protects all session writes behind `sessionWriteMu sync.Mutex` using dedicated helpers:
  - `sendRealtimeSafe(in genai.LiveRealtimeInput)`
  - `sendClientContentSafe(in genai.LiveClientContentInput)`
  - `sendToolResponseSafe(in genai.LiveToolResponseInput)`

### 9.4 Model & Tool Sandbox Isolation
* **The Invariant**: `liveCfg.Tools` is strictly populated inside `if !isRobotics`. Desktop macOS tool schemas are never leaked into physical robotics sessions (`gemini-robotics-er-2-streaming-preview`).

### 9.5 Alternating Turn Role Coalescing in Cold Resumption
* **The Invariant**: When assembling historical turns from `sessions/<id>.json`, adjacent turns of identical role are merged (`strings.TrimSpace(lastPart.Text + "\n" + t.Text)`), guaranteeing strictly alternating `user` $\to$ `model` $\to$ `user` structure required by `session.SendClientContent`.

### 9.6 GoAway Socket Rotation vs. Final Disconnect Differentiation
* **The Invariant**: Lifel tracks `sawGoAway bool`. If `sawGoAway` is true (or client sends `warm_rotate`), memory consolidation is suppressed so that Trigger B only fires upon genuine user disconnection.

### 9.7 Pinned Core Fact Anti-Duplication Prompt Architecture
* **The Invariant**: Pinned identity facts are isolated in Layer 4 of the system instruction and kept strictly under 400 tokens. The Flash memory curation prompt separates existing candidate blocks from new turns, preventing circular duplication.

---

## 10. Server-Side Sliding Window Compaction

In `live_ws.go`, Lifel configures Google's server-side **Context Window Compression**:
* **`TriggerTokens: 80,000`**
* **`TargetTokens: 40,000`**

```
  0 Tokens ────────────────────────────────────────► 80,000 Tokens (TRIGGER HIT)
                                                            │
  ┌─────────────────────────────────────────────────────────▼────────────────┐
  │ Google's Server Compaction Logic:                                        │
  │                                                                          │
  │  [System Instruction & Tools]  ======> PROTECTED (Never Evicted)         │
  │  [Oldest Conversation Audio]   ======> EVICTED (Freed from TPU Memory)   │
  │  [Recent Turns & KV-State]     ======> RETAINED                          │
  └─────────────────────────────────────────────────────────┬────────────────┘
                                                            │
                                                            ▼
                                                      40,000 Tokens (New Baseline)
```

Because **Trigger A** snapshots and indexes unindexed turns at **55,000 tokens** (well before the 80,000 token eviction mark), **zero conversational detail is ever lost** when Google evicts raw audio tokens from the TPU memory.

---

## 11. Two-Tier Resumption Architecture

Past context survives interruptions, disconnects, and restarts through a two-tier strategy:

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                            TWO-TIER RESUMPTION                               │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  [Client Connects]                                                           │
│         │                                                                    │
│         ▼                                                                    │
│  Has Valid Handle? (Within 2 Hours)                                          │
│    ├──► YES: Try TIER A (Warm Resumption) ──► Restores TPU Cache (0 Tokens)  │
│    │               │ (If Failed or Expired)                                  │
│    │               ▼                                                         │
│    └──► NO:  Use TIER B (Cold Resumption) ──► Seed via SendClientContent     │
│                                                (Mutable KV-Cache, 20 Turns)  │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Tier A: Warm Resumption (`SessionResumptionConfig`)
* Resumption handles remain valid in Google's cache for **2 hours** after session termination.
* Lifel saves `last_resumption_handle` only when `resp.SessionResumptionUpdate.Resumable == true` and `NewHandle != ""`.
* When reconnecting within 2 hours, passing `SessionResumptionConfig{Handle: lastHandle}` restores the entire TPU KV-cache at **0 extra tokens**.

### Tier B: Cold Resumption (Seeding via `SendClientContent`)
* For calls older than 2 hours, Lifel reads the last 20 turns from `sessions/<id>.json`, coalesces adjacent identical roles, and seeds them into the mutable KV-cache via `session.SendClientContent(Turns, TurnComplete: false)`.
* This ensures old turns are naturally compressible by the sliding window and never permanently freeze in `SystemInstruction`.

---

## 12. Context Architecture Comparison Matrix

| Capability | Standard Chat REST APIs | Prior Naive Lifel Implementation | Lifel Production RAG Architecture |
| :--- | :--- | :--- | :--- |
| **Context Location** | Re-sent by client on every HTTP request | Server KV-Cache + flat memory list | Server KV-Cache + Local Vector RAG (`memory_rag.go`) |
| **Vision Ingestion** | Static snapshots | Web stream + desktop screen recording | 1–2 FPS Webcam Camera stream (screen capture stripped) |
| **Long-Term Memory** | Static system prompt or raw SQL | Flat 25-item JSON list in system prompt | Deep Local Semantic RAG (`gemini-embedding-2`, 3072-dim) |
| **System Prompt Layer 4** | Unbounded | Injected 25 one-liners ($\sim 500\text{ tokens}$) | Strictly $< 400\text{ tokens}$ (pinned identity + project index) |
| **Summarization Timing** | Stateless | Disconnect only (risked data loss at 80k) | Deterministic Trigger A (55k/16 turns) + Trigger B (disconnect) |
| **Memory Mutation Loop** | Manual | Append-only consolidation | Autonomous Flash CRUD Controller (`INSERT`, `MERGE`, `UPDATE`, `DELETE`) |
| **Memory Tool** | None | Separate `save_user_memory` & `search_user_memory` | Unified `memory` tool (`recall`, `remember`, `update`, `forget`) |
| **Tool Scheduling** | Model halts until tool returns | `WHEN_IDLE` for all | `WHEN_IDLE` for `recall`, `SILENT` for async mutations |
| **Max Call Duration** | Degrades linearly | 80k/40k sliding window | 80k/40k sliding window backed by pre-compaction RAG index |
| **Resumption** | Resend full history | Unconditional handle overwriting | Two-Tier Resumption (2h Warm Handle + Cold `SendClientContent`) |
