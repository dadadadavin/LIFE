# Lifel Context Management Architecture

This document explains **strictly and exclusively** how conversation context, token lifecycles, and memory persistence are managed across Google Gemini Live and local storage.

---

## 1. The 4 Layers of Context

Context is divided into four distinct memory tiers, ranging from raw GPU KV-cache in memory to persistent storage on disk:

```
┌────────────────────────────────────────────────────────────────────────┐
│  Tier 1: Ephemeral In-Flight Context (Google Server RAM / KV-Cache)    │
│  - Live streaming audio, user speech, video frames, tool calls         │
│  - Token window: 0 → 80,000 tokens                                     │
└──────────────────────────────────┬─────────────────────────────────────┘
                                   │
                                   ▼ (If tokens exceed 80,000)
┌────────────────────────────────────────────────────────────────────────┐
│  Tier 2: Server-Side Sliding Window Compression                        │
│  - Triggers at 80,000 tokens → Compresses context down to 40,000        │
│  - Older audio/vision tokens dropped; system prompt preserved          │
└──────────────────────────────────┬─────────────────────────────────────┘
                                   │
                                   ▼ (When connection drops / closes)
┌────────────────────────────────────────────────────────────────────────┐
│  Tier 3: Dual-Layer Resumption Engine                                  │
│  - Layer 1 (Warm / Minutes): Google SessionResumption handle (KV-Cache)│
│  - Layer 2 (Cold / Days): Injects last 20 turns from sessions/*.json   │
└──────────────────────────────────┬─────────────────────────────────────┘
                                   │
                                   ▼ (Post-Session Distillation)
┌────────────────────────────────────────────────────────────────────────┐
│  Tier 4: Permanent Memory Bank (memory.json)                           │
│  - Distilled facts, durable preferences, and rolling session summary   │
│  - Injected into every new session prompt (~200–500 tokens)            │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. In-Flight Context Window & Compression

In a live voice/vision session, tokens accumulate rapidly (audio PCM chunks, JPEG vision frames, tool roundtrips, and model speech).

### Token Allocation & Settings (`config.json`)
* **Trigger Tokens (`trigger_tokens`)**: `80,000`
* **Target Tokens (`target_tokens`)**: `40,000`

### How Compression Works
Configured in `live_ws.go` via Google's `ContextWindowCompressionConfig`:
```go
liveCfg.ContextWindowCompression = &genai.ContextWindowCompressionConfig{
    TriggerTokens: &triggerTokens, // 80,000
    SlidingWindow: &genai.SlidingWindow{
        TargetTokens: &targetTokens, // 40,000
    },
}
```
* When cumulative session tokens hit **80,000**, Google's server automatically evicts the oldest conversation turns until context size drops to **40,000**.
* **What is preserved**: The original System Instruction, Tool schemas, and the most recent turns.
* **What is discarded**: Oldest intermediate raw audio frames and interim tool outputs.
* **Why this matters**: Keeps latency constant ($<100\text{ms}$) and prevents model degradation or out-of-memory crashes on long calls.

---

## 3. Dual-Layer Resumption Engine

Live sessions are ephemeral WebSockets. If a call drops or you disconnect, the system uses two distinct layers to recover context:

### Layer 1: Native Neural Resumption (Warm Cache)
* **Storage**: In-memory KV-cache on Google's servers.
* **Mechanism**: During the call, Google continuously streams `SessionResumptionUpdate{NewHandle: "uuid"}`.
* Go intercepts this and writes the UUID into `config.json` (`last_resumption_handle`).
* If you reconnect within minutes, Go passes `SessionResumptionConfig{Handle: lastResumptionHandle}` in the handshake.
* **Result**: Zero token recalculation. Gemini resumes speaking with full memory of the conversation.
* **Limitation**: Google expires these handles after **5 to 15 minutes of inactivity**.

### Layer 2: Disk Transcript Injection (Cold Resumption)
* **Storage**: Local JSON files in `sessions/<timestamp>.json`.
* **Trigger**: Used when Layer 1 handle has expired, or when you click "Resume" on an old session from yesterday or last week.
* **Mechanism**:
  1. Go loads the target session record from `sessions/<session_id>.json`.
  2. Slices the **last 20 conversation turns** (`User` and `Gemini`).
  3. Formats them into a structured text block:
     ```
     === RESUMED CONVERSATION CONTEXT (Session: 20261106_185935) ===
     User: What can you help me with?
     Gemini: I can manage your Mac, search the web, and take notes.
     ...
     === END RESUMED CONTEXT ===
     ```
  4. Injects this block directly into the System Instruction of the new connection.
* **Result**: Gemini greets you knowing the full history of the old call, even after a computer restart.
* **Cost**: Consumes $\sim 1,000\text{--}3,000$ input tokens on the initial handshake.

---

## 4. The Persistent Memory Bank (`memory.json`)

While conversation transcripts stay in `sessions/`, durable knowledge about you is distilled into `memory.json`.

### How Facts Enter Memory
1. **In-Flight (Real-time)**: You say *"Remember that my drone runs on Betaflight"*. Gemini triggers the `save_user_memory` tool. Go writes it to `memory.json` immediately.
2. **Post-Session (Distillation Cascade)**: When a call ends with $\ge 2$ turns, Go dispatches the full transcript to `gemini-3.5-flash-lite`:
   * Generates a 1–2 sentence **rolling session summary**.
   * Extracts durable facts (preferences, projects, hardware).
   * Overwrites the previous session summary and appends new facts to `memory.json`.

### How Memory Enters Context
At the start of every connection, Go reads `memory.json` and injects:
```
=== PERSISTENT USER MEMORY BANK ===
- [PINNED] [SUMMARY] Last session (20261106_185935): Discussed robotics vision setup.
- [PINNED] [PREFERENCE] User prefers direct, concise answers with zero AI slop.
- [USER] User works on macOS Apple Silicon in the local workspace.
===================================
```
* **Footprint**: Only $\sim 200\text{--}500$ tokens.
* **Behavior**: Gemini never asks you to repeat your preferences or past decisions.

---

## 5. Token Accounting & Telemetry

Context consumption is tracked and surfaced through `UsageMetadata`:

| Token Metric | Source | Meaning |
| :--- | :--- | :--- |
| `PromptTokens` | Input | System instructions + Memory Bank + Resumed turns + Audio chunks + Tool schemas |
| `ResponseTokens` | Output | Audio synthesis + text transcription tokens emitted by Gemini |
| `TotalTokens` | Total | Total active context size currently resident in the session |

* Every turn updates `latestUsage` in Go.
* When the session closes, the final token count is permanently saved in the session header (`sessions/<id>.json`).

---

## 6. Context Reset & Clean Slate

To wipe all context and return to a zero-token baseline:
1. **Clear `memory.json`**: Set to `[]` (removes all persistent facts and rolling summaries).
2. **Clear `last_resumption_handle`**: Set to `""` in `config.json` (prevents warm KV-cache restoration).
3. **Set `continue_last_session`**: Set to `false` (prevents Layer 2 cold transcript injection).

Once cleared, the next session starts strictly with the base system instructions ($\approx 1,800$ tokens for schemas and base instructions).
