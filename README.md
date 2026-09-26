# LIFE — Gemini 3.8 Live Real-Time Voice & Vision Workbench

A low-latency, real-time multimodal voice, vision, and local tool workbench powered by **Go (`google.golang.org/genai`)**, **HTMX**, and **Tailwind CSS v4** for **Gemini 3.8 Live (`gemini-3.8-live`)**.

## Features

- **Real-Time Bidirectional Voice (`16kHz` PCM In / `24kHz` PCM Out)**:
  - Native low-latency WebSocket audio bridge (`/ws/live`).
  - Hardware **Echo Shield** with configurable **Voice Barge-In** threshold and keyboard shortcuts (`Space` to interrupt speech, `M` to mute mic).
  - Bilingual Speech Recognition (`en-US`, `id-ID`) with custom domain vocabulary biasing.
- **High-Resolution Vision (`640p` / `1080p` / `1280p`)**:
  - Live webcam and screen sharing at 1 FPS plus on-demand crisp HD frame snapshots (`capture_mac_screen`).
- **12 Local System & Multi-Source Web Tools**:
  - `web_search` (DuckDuckGo HTML + Google News RSS + English & Indonesian Wikipedia)
  - `read_webpage` (clean full-article text extractor)
  - `get_mac_system_info`, `mac_clipboard`, `open_url_or_app`, `capture_mac_screen`
  - `save_user_memory`, `search_user_memory`, `list_project_files`, `read_project_file`, `write_project_note`, `calculate_math`
- **Persistent Memory Bank & Session Continuation**:
  - Pinned memories, inline editing, automatic post-session memory extraction & rolling session summaries (`gemini-3.5-flash`), and one-click session resumption.
- **Multi-Key Failover Rotation**:
  - Supports multiple `GEMINI_API_KEY_*` slots in `.env` with automatic failover on quota or connection errors.

## Quick Start

1. **Configure API Keys**:
   ```bash
   cp .env.example .env
   ```
   Add your Gemini API key(s) inside `.env` (`.env` is git-ignored and never committed).

2. **Build & Run (Go)**:
   ```bash
   go build -o lifel .
   ./lifel
   ```
   Then open `http://localhost:8000` in Chrome.
