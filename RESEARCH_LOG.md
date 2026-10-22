# Gemini Live Research & Engineering Log

> **Project Mandate**: This repository is an applied engineering research initiative investigating Google's Gemini Multimodal Live API (`gemini-3.8-live`, `gemini-3.8-flash`) via the official `google.golang.org/genai` SDK and real-time bidirectional WebSockets. The objective is not merely building a chat utility, but identifying the architectural paradigms, audio DSP pipelines, VAD tuning, and UX patterns required to achieve truly natural, frictionless human-AI voice interaction.

---

## Milestone: Codename `greenfield`
- **Status**: Stable Benchmark Architecture
- **Date**: October 2026
- **Model Target**: `gemini-3.8-live` (multimodal bidirectional live) + `gemini-3.8-flash` (post-session extraction)
- **Git Checkpoint Tag**: `v1.0.0-stable` / commit `b3e81c2`

### Key Research Findings & Breakthroughs

#### 1. The Speech Onset Barge-In Problem (Truncation Bug)
- **Phenomenon**: When the user interrupted the AI mid-sentence (e.g. saying *"Wait, wait, what I want to ask is ABC"*), the beginning of the utterance was dropped, transmitting only the tail (*"is ABC"*), or dropping numbers when counting (*"1 2 3 ... 10"* -> *"4 5 ... 10"*).
- **Root Cause Identified**:
  1. *Echo Shield Silence Substitution*: During model speech playback, incoming mic buffers were being zeroed out (`silentFrame`) to prevent speaker feedback.
  2. *Excessive RMS Threshold*: The barge-in threshold was set to `2800`–`3200` RMS, whereas typical conversational speech into a MacBook microphone is only `400`–`1200` RMS.
  3. *Multi-Frame Lag*: A requirement for 2 consecutive hot frames before triggering barge-in meant the first `128ms`+ of speech was unconditionally discarded.
  4. *Cooldown Suppression*: A 220ms post-playback cooldown was silencing the mic right as the user started speaking.
  5. *UI Bubble Orphaning*: The client-side WebSocket handler nulled out `currentUserBubble` upon receiving the `interrupted` event, breaking transcription continuity.

#### 2. The `greenfield` Solution Pipeline
1. **Pre-Roll Rolling Buffer**:
   - Maintained a rolling 3-chunk (`384ms` total at 128ms/2048-sample frames) ring buffer of unmuted raw audio.
   - When barge-in triggers, all 3 pre-roll chunks are flushed immediately before the current frame, completely eliminating clipped onset syllables (*"Wait"*, *"Can you"*, *"1, 2"*).
2. **Instant Single-Frame Barge-In**:
   - Lowered default `barge_in_threshold` to `600` RMS.
   - Barge-in triggers on the very first hot frame without multi-frame lag.
   - Instantly halts speaker playback (`stopAllPlayback()`) and resets `lastSpeakerEndTime = 0` to disable cooldown muting.
3. **Server-Side VAD Prefix Padding**:
   - Configured `PrefixPaddingMs` in `AutomaticActivityDetection` to `300ms` (up from 200ms), giving Gemini's server-side neural VAD adequate onset context.
4. **Interim Transcription State Transition**:
   - In `live_ws.go`, reset `modelSpeaking = false` and cleared `userTextBuf` on the very first `sc.InterimInputTranscription` event to prevent turn bleeding.

---

## Core System Architecture & UX Inquiries

### 1. Dual-Layer Context Resumption
Live sessions are inherently ephemeral. If a network drops or a user disconnects, maintaining conversational continuity requires a dual-layer strategy:
- **Layer 1 (Native Neural Resumption)**: Handled via Google's `SessionResumptionConfig{Handle: lastResumptionHandle}`. Allows zero-loss continuation of the neural conversation state on Google's servers.
- **Layer 2 (Dual-Layer Context Fallback)**: Google resumption handles expire after several minutes of inactivity. When reconnecting to an expired or previous session, Lifel loads the last 20 conversation turns from `sessions/<session_id>.json` and injects them into the system instruction (`=== RESUMED CONVERSATION CONTEXT ===`), giving Gemini instant historical memory.

### 2. Autonomous Memory Architecture
To avoid asking the user to repeat facts, memory is split into:
- **In-Session Tool Manipulation**: Gemini has real-time access to `save_user_memory` and `search_user_memory`. Changes update `memory.json` and push instant HTMX DOM updates to the UI.
- **Post-Session Distillation**: Once a session terminates, a background goroutine dispatches the full transcript to `gemini-3.8-flash`. It generates a 1-2 sentence rolling summary of the session and extracts durable user facts, preferences, and project context.
- **Prompt Injection**: At the start of every connection, all active items in `memory.json` are prepended into the system prompt (`=== PERSISTENT USER MEMORY BANK ===`).

### 3. Transparent Observability
- All events (VAD start/end, tool invocations, audio turn latencies, interim transcripts, token metrics) are logged transparently to stdout in ANSI color.
- Latency from user speech completion to model first-byte audio is tracked and surfaced to the UI.

---

## Research Experiment Log

| Date | Codename | Focus Area | Outcome / Key Observation |
| :--- | :--- | :--- | :--- |
| **2026-10-12** | `greenfield` | Speech Barge-In & Pre-Roll Audio | Implemented 384ms pre-roll ring buffer, lowered barge-in threshold to 600 RMS, eliminated onset truncation, preserved transcription continuity. Best overall user experience achieved. |
| *Future* | *TBD* | Tool Scheduling (`WHEN_IDLE` vs `INTERRUPT`) | Evaluating UX trade-offs between asynchronous background tool execution and immediate vocal interruption. |
| *Future* | *TBD* | Dynamic Rate-Limited Vision Ingestion | Investigating adaptive frame-rate scaling for screen capture based on UI motion detection. |
