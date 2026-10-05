package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/genai"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func init() {
	var stdDialer = &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	websocket.DefaultDialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Force IPv4 for outbound Live API connections to eliminate ISP IPv6 route flapping
		return stdDialer.DialContext(ctx, "tcp4", addr)
	}
}

func buildLiveConnectConfig(cfg AppConfig, activeKeyName string) *genai.LiveConnectConfig {
	temp := cfg.Temperature
	prefixMs := cfg.PrefixPaddingMs
	silenceMs := cfg.SilenceDurationMs
	triggerTokens := cfg.TriggerTokens
	targetTokens := cfg.TargetTokens

	fullPrompt := buildFullSystemPrompt(cfg, activeKeyName)

	liveCfg := &genai.LiveConnectConfig{
		ResponseModalities: []genai.Modality{genai.ModalityAudio},
		Temperature:        &temp,
		SpeechConfig: &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{
					VoiceName: cfg.VoiceName,
				},
			},
		},
		InputAudioTranscription:  &genai.AudioTranscriptionConfig{},
		OutputAudioTranscription: &genai.AudioTranscriptionConfig{},
		RealtimeInputConfig: &genai.RealtimeInputConfig{
			ActivityHandling: genai.ActivityHandling(cfg.ActivityHandling),
			TurnCoverage:     genai.TurnCoverage(cfg.TurnCoverage),
			AutomaticActivityDetection: &genai.AutomaticActivityDetection{
				StartOfSpeechSensitivity: genai.StartSensitivity(cfg.StartSensitivity),
				EndOfSpeechSensitivity:   genai.EndSensitivity(cfg.EndSensitivity),
				PrefixPaddingMs:          &prefixMs,
				SilenceDurationMs:        &silenceMs,
			},
		},
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: fullPrompt}},
		},
	}

	if cfg.ProactiveAudio {
		proactive := true
		liveCfg.Proactivity = &genai.ProactivityConfig{
			ProactiveAudio: &proactive,
		}
	}

	if cfg.SessionResumption {
		resCfg := &genai.SessionResumptionConfig{
			Transparent: true,
		}
		if cfg.ContinueLastSession && cfg.LastResumptionHandle != "" {
			resCfg.Handle = cfg.LastResumptionHandle
		}
		liveCfg.SessionResumption = resCfg
	}

	if cfg.ContextCompression {
		liveCfg.ContextWindowCompression = &genai.ContextWindowCompressionConfig{
			TriggerTokens: &triggerTokens,
			SlidingWindow: &genai.SlidingWindow{
				TargetTokens: &targetTokens,
			},
		}
	}

	tools := buildGenAITools(cfg)
	if len(tools) > 0 {
		liveCfg.Tools = tools
	}

	return liveCfg
}

func handleLiveWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("%s \033[31m[WS UPGRADE ERROR]\033[0m %v (remote: %s)\n", time.Now().Format("15:04:05"), err, r.RemoteAddr)
		return
	}
	defer conn.Close()

	var wsMu sync.Mutex
	wsClosed := false
	safeSend := func(payload map[string]any) error {
		wsMu.Lock()
		defer wsMu.Unlock()
		if wsClosed {
			return fmt.Errorf("websocket closed")
		}
		return conn.WriteJSON(payload)
	}

	cfg := loadConfig()
	if vOverride := r.URL.Query().Get("voice"); vOverride != "" {
		cfg.VoiceName = vOverride
	}
	if r.URL.Query().Get("resume") == "true" {
		cfg.ContinueLastSession = true
	}

	fmt.Printf("\n%s \033[1;34m[WS CONNECT]\033[0m Client connected from %s (Voice: %s, Resume: %v)\n",
		time.Now().Format("15:04:05"), r.RemoteAddr, cfg.VoiceName, cfg.ContinueLastSession)

	orderedKeys := getOrderedAPIKeys(cfg.SelectedKey, cfg.AutoKeyFailover)
	if len(orderedKeys) == 0 {
		errMsg := "No Gemini API keys configured in .env. Please add a key in the API Keys tab."
		fmt.Printf("%s \033[31m[WS ERROR]\033[0m %s\n", time.Now().Format("15:04:05"), errMsg)
		_ = safeSend(map[string]any{
			"type":    "error",
			"message": errMsg,
		})
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var session *genai.Session
	var connectedKeyName string
	var connectedKeyVal string
	var lastConnErr error

	for _, k := range orderedKeys {
		fmt.Printf("%s \033[34m[GEMINI]\033[0m Handshaking with %s using key slot %s...\n",
			time.Now().Format("15:04:05"), cfg.Model, k.Name)
		_ = safeSend(map[string]any{
			"type":    "status",
			"message": fmt.Sprintf("Connecting to %s using %s...", cfg.Model, k.Name),
		})

		clientCfg := &genai.ClientConfig{
			APIKey:  k.Value,
			Backend: genai.BackendGeminiAPI,
		}
		if cfg.ProactiveAudio {
			clientCfg.HTTPOptions = genai.HTTPOptions{APIVersion: "v1alpha"}
		}
		client, err := genai.NewClient(ctx, clientCfg)
		if err != nil {
			lastConnErr = err
			fmt.Printf("%s \033[33m[GEMINI WARNING]\033[0m Key %s client creation error: %v\n", time.Now().Format("15:04:05"), k.Name, err)
			continue
		}

		liveCfg := buildLiveConnectConfig(cfg, k.Name)
		sess, err := client.Live.Connect(ctx, cfg.Model, liveCfg)
		if err != nil && liveCfg.SessionResumption != nil && liveCfg.SessionResumption.Handle != "" {
			fmt.Printf("%s \033[33m[GEMINI RESUMPTION]\033[0m Handle resume failed (%v), retrying without handle using injected context...\n", time.Now().Format("15:04:05"), err)
			liveCfg.SessionResumption.Handle = ""
			sess, err = client.Live.Connect(ctx, cfg.Model, liveCfg)
		}
		if err != nil {
			lastConnErr = err
			fmt.Printf("%s \033[33m[GEMINI WARNING]\033[0m Key %s connection failed: %v\n", time.Now().Format("15:04:05"), k.Name, err)
			_ = safeSend(map[string]any{
				"type":    "status",
				"message": fmt.Sprintf("Key %s failed (%v), trying fallback...", k.Name, err),
			})
			continue
		}

		session = sess
		connectedKeyName = k.Name
		connectedKeyVal = k.Value
		break
	}

	if session == nil {
		errMsg := "All API keys failed to connect"
		if lastConnErr != nil {
			errMsg = fmt.Sprintf("All API keys failed. Last error: %v", lastConnErr)
		}
		fmt.Printf("%s \033[31m[GEMINI ERROR]\033[0m %s\n", time.Now().Format("15:04:05"), errMsg)
		_ = safeSend(map[string]any{
			"type":    "error",
			"message": errMsg,
		})
		return
	}
	defer session.Close()

	fmt.Printf("%s \033[1;32m[GEMINI SUCCESS]\033[0m Live session ready! Model: %s | Voice: %s | Key: %s\n",
		time.Now().Format("15:04:05"), cfg.Model, cfg.VoiceName, connectedKeyName)

	// Clear one-time resume_session_id once connected
	if cfg.ResumeSessionID != "" {
		_ = saveConfigMap(map[string]any{"resume_session_id": ""})
	}

	sessionStart := time.Now()
	sessionID := sessionStart.Format("20060102_150405")
	startedAt := sessionStart.Format(time.RFC3339)

	var logMu sync.Mutex
	var turnsLog []TurnEntry
	var latestResumptionHandle string
	var latestUsage UsageStats

	defer func() {
		dur := time.Since(sessionStart).Seconds()
		logMu.Lock()
		numTurns := len(turnsLog)
		tokens := latestUsage.TotalTokens
		logMu.Unlock()
		fmt.Printf("\n%s \033[1;34m[WS DISCONNECT]\033[0m Session %s ended. Duration: %.1fs | Turns: %d | Tokens: %d\n",
			time.Now().Format("15:04:05"), sessionID, dur, numTurns, tokens)
	}()

	var turnMu sync.Mutex
	var userTextBuf []string
	var modelTextBuf []string
	var lastUserActivity time.Time
	var turnFirstByteLatencyMs int64
	var modelSpeaking bool

	_ = safeSend(map[string]any{
		"type":     "connected",
		"model":    cfg.Model,
		"voice":    cfg.VoiceName,
		"key_name": connectedKeyName,
	})

	doneCh := make(chan struct{})
	var onceClose sync.Once
	signalDone := func() {
		onceClose.Do(func() {
			cancel()
			close(doneCh)
		})
	}

	// Goroutine 1: Browser -> Gemini Live
	go func() {
		defer signalDone()
		for {
			_, rawMsg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(rawMsg, &msg); err != nil {
				continue
			}
			mType, _ := msg["type"].(string)

			switch mType {
			case "audio":
				b64, _ := msg["data"].(string)
				if b64 == "" {
					continue
				}
				pcmBytes, err := base64.StdEncoding.DecodeString(b64)
				if err != nil || len(pcmBytes) == 0 {
					continue
				}
				isSilent, _ := msg["silent"].(bool)
				if !isSilent {
					turnMu.Lock()
					lastUserActivity = time.Now()
					turnMu.Unlock()
				}

				_ = session.SendRealtimeInput(genai.LiveRealtimeInput{
					Audio: &genai.Blob{
						Data:     pcmBytes,
						MIMEType: "audio/pcm;rate=16000",
					},
				})

			case "audio_stream_end":
				_ = session.SendRealtimeInput(genai.LiveRealtimeInput{
					AudioStreamEnd: true,
				})

			case "image":
				b64, _ := msg["data"].(string)
				if b64 == "" {
					continue
				}
				jpegBytes, err := base64.StdEncoding.DecodeString(b64)
				if err != nil || len(jpegBytes) == 0 {
					continue
				}
				mimeType, _ := msg["mime_type"].(string)
				if mimeType == "" {
					mimeType = "image/jpeg"
				}
				fmt.Printf("%s \033[36m[VISION]\033[0m Ingested frame (%d bytes, %s)\n",
					time.Now().Format("15:04:05"), len(jpegBytes), mimeType)
				_ = session.SendRealtimeInput(genai.LiveRealtimeInput{
					Video: &genai.Blob{
						Data:     jpegBytes,
						MIMEType: mimeType,
					},
				})

			case "text":
				textVal, _ := msg["text"].(string)
				textVal = strings.TrimSpace(textVal)
				if textVal == "" {
					continue
				}
				fmt.Printf("%s \033[1;36m[USER TEXT]\033[0m %s\n", time.Now().Format("15:04:05"), textVal)
				nowStr := time.Now().Format("15:04:05")
				logMu.Lock()
				turnsLog = append(turnsLog, TurnEntry{
					Role: "user",
					Text: textVal,
					Time: nowStr,
				})
				logMu.Unlock()

				turnMu.Lock()
				lastUserActivity = time.Now()
				turnFirstByteLatencyMs = 0
				turnMu.Unlock()

				_ = safeSend(map[string]any{
					"type": "user_text_committed",
					"text": textVal,
				})

				turnComplete := true
				_ = session.SendClientContent(genai.LiveClientContentInput{
					Turns: []*genai.Content{
						{
							Role:  "user",
							Parts: []*genai.Part{{Text: textVal}},
						},
					},
					TurnComplete: &turnComplete,
				})

			case "ping":
				_ = safeSend(map[string]any{"type": "pong", "ts": msg["ts"]})

			case "disconnect":
				return
			}
		}
	}()

	// Goroutine 2: Gemini Live -> Browser
	go func() {
		defer signalDone()
		for {
			resp, err := session.Receive()
			if err != nil {
				if ctx.Err() == nil {
					fmt.Printf("%s \033[31m[GEMINI STREAM CLOSED]\033[0m %v\n", time.Now().Format("15:04:05"), err)
					_ = safeSend(map[string]any{
						"type":    "error",
						"message": fmt.Sprintf("Gemini stream closed: %v", err),
					})
				}
				return
			}
			if resp == nil {
				continue
			}

			// Update #5: Persist Session Resumption Handle to config.json asynchronously
			if resp.SessionResumptionUpdate != nil && resp.SessionResumptionUpdate.NewHandle != "" {
				newHandle := resp.SessionResumptionUpdate.NewHandle
				logMu.Lock()
				latestResumptionHandle = newHandle
				logMu.Unlock()
				fmt.Printf("%s \033[35m[RESUMPTION]\033[0m Handle saved: %s\n", time.Now().Format("15:04:05"), newHandle)
				go func(h string) {
					_ = saveConfigMap(map[string]any{"last_resumption_handle": h})
				}(newHandle)
				_ = safeSend(map[string]any{
					"type":   "resumption_update",
					"handle": newHandle,
				})
			}

			// Update #6: Capture & Broadcast Real-Time Token UsageMetadata
			if resp.UsageMetadata != nil && resp.UsageMetadata.TotalTokenCount > 0 {
				u := UsageStats{
					PromptTokens:   resp.UsageMetadata.PromptTokenCount,
					ResponseTokens: resp.UsageMetadata.ResponseTokenCount,
					TotalTokens:    resp.UsageMetadata.TotalTokenCount,
				}
				logMu.Lock()
				latestUsage = u
				logMu.Unlock()
				_ = safeSend(map[string]any{
					"type":            "usage",
					"prompt_tokens":   u.PromptTokens,
					"response_tokens": u.ResponseTokens,
					"total_tokens":    u.TotalTokens,
				})
			}

			if sc := resp.ServerContent; sc != nil {
				// 1. Input Audio Transcription (User speech)
				if sc.InterimInputTranscription != nil && sc.InterimInputTranscription.Text != "" {
					tx := sc.InterimInputTranscription.Text
					turnMu.Lock()
					lastUserActivity = time.Now()
					turnFirstByteLatencyMs = 0
					fullUser := strings.TrimSpace(strings.Join(userTextBuf, ""))
					if fullUser != "" {
						fullUser += " " + tx
					} else {
						fullUser = tx
					}
					turnMu.Unlock()

					_ = safeSend(map[string]any{
						"type":    "input_tx",
						"text":    tx,
						"full":    fullUser,
						"interim": true,
					})
				}

				if sc.InputTranscription != nil && sc.InputTranscription.Text != "" {
					tx := sc.InputTranscription.Text
					turnMu.Lock()
					if modelSpeaking {
						// Transition from model response to new user turn
						modelSpeaking = false
						userTextBuf = nil
					}
					userTextBuf = append(userTextBuf, tx)
					lastUserActivity = time.Now()
					turnFirstByteLatencyMs = 0
					fullUser := strings.TrimSpace(strings.Join(userTextBuf, ""))
					turnMu.Unlock()

					_ = safeSend(map[string]any{
						"type":  "input_tx",
						"text":  tx,
						"full":  fullUser,
						"final": sc.InputTranscription.Finished,
					})
				}

				// 2. Output Audio Transcription (Gemini spoken words)
				if sc.OutputTranscription != nil && sc.OutputTranscription.Text != "" {
					tx := sc.OutputTranscription.Text
					var flushedUser string
					turnMu.Lock()
					if !modelSpeaking {
						modelSpeaking = true
						if len(userTextBuf) > 0 {
							flushedUser = strings.TrimSpace(strings.Join(userTextBuf, ""))
							userTextBuf = nil
						}
					}
					if turnFirstByteLatencyMs == 0 && !lastUserActivity.IsZero() {
						turnFirstByteLatencyMs = time.Since(lastUserActivity).Milliseconds()
					}
					modelTextBuf = append(modelTextBuf, tx)
					fullModel := strings.TrimSpace(strings.Join(modelTextBuf, ""))
					turnMu.Unlock()

					if flushedUser != "" {
						fmt.Printf("%s \033[1;36m[USER]\033[0m %s\n", time.Now().Format("15:04:05"), flushedUser)
						logMu.Lock()
						turnsLog = append(turnsLog, TurnEntry{
							Role: "user",
							Text: flushedUser,
							Time: time.Now().Format("15:04:05"),
						})
						logMu.Unlock()

						_ = safeSend(map[string]any{
							"type": "user_turn_finalized",
							"text": flushedUser,
						})
					}

					_ = safeSend(map[string]any{
						"type":  "output_tx",
						"text":  tx,
						"full":  fullModel,
						"final": sc.OutputTranscription.Finished,
					})
				}

				// 3. Model Turn Audio Parts (Strictly ignore thought text parts!)
				if sc.ModelTurn != nil {
					for _, part := range sc.ModelTurn.Parts {
						if part.InlineData != nil && len(part.InlineData.Data) > 0 {
							turnMu.Lock()
							if turnFirstByteLatencyMs == 0 && !lastUserActivity.IsZero() {
								turnFirstByteLatencyMs = time.Since(lastUserActivity).Milliseconds()
							}
							turnMu.Unlock()

							b64Audio := base64.StdEncoding.EncodeToString(part.InlineData.Data)
							_ = safeSend(map[string]any{
								"type": "audio",
								"data": b64Audio,
							})
						}
					}
				}

				// 4. Interruption
				if sc.Interrupted {
					turnMu.Lock()
					flushedModel := strings.TrimSpace(strings.Join(modelTextBuf, ""))
					modelTextBuf = nil
					modelSpeaking = false
					turnMu.Unlock()

					if flushedModel != "" {
						fmt.Printf("%s \033[33m[INTERRUPTED]\033[0m Gemini speech interrupted: \"%s\"\n",
							time.Now().Format("15:04:05"), flushedModel)
						logMu.Lock()
						turnsLog = append(turnsLog, TurnEntry{
							Role: "model",
							Text: flushedModel + " [Interrupted]",
							Time: time.Now().Format("15:04:05"),
						})
						logMu.Unlock()
					}
					_ = safeSend(map[string]any{"type": "interrupted"})
				}

				// 5. Turn Complete
				if sc.TurnComplete {
					turnMu.Lock()
					flushedUser := strings.TrimSpace(strings.Join(userTextBuf, ""))
					userTextBuf = nil
					flushedModel := strings.TrimSpace(strings.Join(modelTextBuf, ""))
					modelTextBuf = nil
					modelSpeaking = false
					latencyMs := turnFirstByteLatencyMs
					turnFirstByteLatencyMs = 0
					turnMu.Unlock()

					nowStr := time.Now().Format("15:04:05")
					logMu.Lock()
					if flushedUser != "" {
						fmt.Printf("%s \033[1;36m[USER]\033[0m %s\n", nowStr, flushedUser)
						turnsLog = append(turnsLog, TurnEntry{
							Role: "user",
							Text: flushedUser,
							Time: nowStr,
						})
					}
					if flushedModel != "" {
						fmt.Printf("%s \033[1;32m[GEMINI (%s)]\033[0m %s\n", nowStr, cfg.VoiceName, flushedModel)
						turnsLog = append(turnsLog, TurnEntry{
							Role:      "model",
							Text:      flushedModel,
							Time:      nowStr,
							LatencyMs: latencyMs,
						})
					}
					tokensTotal := latestUsage.TotalTokens
					promptTok := latestUsage.PromptTokens
					respTok := latestUsage.ResponseTokens
					logMu.Unlock()

					fmt.Printf("%s \033[36m[LATENCY]\033[0m Turn latency: %dms | Tokens: prompt=%d, resp=%d, total=%d\n",
						nowStr, latencyMs, promptTok, respTok, tokensTotal)

					if flushedUser != "" {
						_ = safeSend(map[string]any{
							"type": "user_turn_finalized",
							"text": flushedUser,
						})
					}

					_ = safeSend(map[string]any{
						"type":       "turn_complete",
						"latency_ms": latencyMs,
					})
				}
			}

			// 6. Tool Calls
			if resp.ToolCall != nil && len(resp.ToolCall.FunctionCalls) > 0 {
				var fnResponses []*genai.FunctionResponse
				for _, fc := range resp.ToolCall.FunctionCalls {
					argsMap := fc.Args
					if argsMap == nil {
						argsMap = map[string]any{}
					}
					argsJSON, _ := json.Marshal(argsMap)
					fmt.Printf("%s \033[1;33m[TOOL CALL]\033[0m %s(%s)\n", time.Now().Format("15:04:05"), fc.Name, string(argsJSON))

					_ = safeSend(map[string]any{
						"type": "tool_call",
						"id":   fc.ID,
						"name": fc.Name,
						"args": argsMap,
					})

					t0 := time.Now()
					resultData := executeLocalTool(fc.Name, argsMap)
					durTool := time.Since(t0)

					// Update #10: Handle browser HD frame request & native macOS screen JPEG injection
					if reqFrame, ok := resultData["_request_browser_hd_frame"].(bool); ok && reqFrame {
						delete(resultData, "_request_browser_hd_frame")
						_ = safeSend(map[string]any{"type": "request_hd_frame"})
					}
					if nativeB64, ok := resultData["_native_screen_b64"].(string); ok && nativeB64 != "" {
						delete(resultData, "_native_screen_b64")
						if jpegBytes, err := base64.StdEncoding.DecodeString(nativeB64); err == nil && len(jpegBytes) > 0 {
							_ = session.SendRealtimeInput(genai.LiveRealtimeInput{
								Video: &genai.Blob{
									Data:     jpegBytes,
									MIMEType: "image/jpeg",
								},
							})
						}
					}

					// Update #7: Notify browser UI when memory or notes are updated via voice tool
					if fc.Name == "save_user_memory" {
						_ = safeSend(map[string]any{"type": "memory_updated"})
					}
					if fc.Name == "write_project_note" {
						_ = safeSend(map[string]any{"type": "notes_updated"})
					}

					nowStr := time.Now().Format("15:04:05")
					resJSON, _ := json.Marshal(resultData)
					fmt.Printf("%s \033[33m[TOOL RESULT]\033[0m %s in %v -> %s\n",
						nowStr, fc.Name, durTool.Truncate(100*time.Microsecond), string(resJSON))

					logMu.Lock()
					turnsLog = append(turnsLog, TurnEntry{
						Role: "tool",
						Text: fmt.Sprintf("%s(%s) -> %s", fc.Name, string(argsJSON), string(resJSON)),
						Time: nowStr,
					})
					logMu.Unlock()

					_ = safeSend(map[string]any{
						"type":   "tool_result",
						"id":     fc.ID,
						"name":   fc.Name,
						"result": resultData,
					})

					sched := genai.FunctionResponseScheduling(cfg.ToolScheduling)
					if sched == "" {
						sched = genai.FunctionResponseSchedulingWhenIdle
					}
					fnResponses = append(fnResponses, &genai.FunctionResponse{
						ID:         fc.ID,
						Name:       fc.Name,
						Response:   map[string]any{"result": resultData},
						Scheduling: sched,
					})
				}

				if len(fnResponses) > 0 {
					_ = session.SendToolResponse(genai.LiveToolResponseInput{
						FunctionResponses: fnResponses,
					})
				}
			}
		}
	}()

	<-doneCh

	wsMu.Lock()
	wsClosed = true
	wsMu.Unlock()

	// Flush any remaining text buffers
	turnMu.Lock()
	remUser := strings.TrimSpace(strings.Join(userTextBuf, ""))
	remModel := strings.TrimSpace(strings.Join(modelTextBuf, ""))
	turnMu.Unlock()

	nowStr := time.Now().Format("15:04:05")
	logMu.Lock()
	if remUser != "" {
		turnsLog = append(turnsLog, TurnEntry{Role: "user", Text: remUser, Time: nowStr})
	}
	if remModel != "" {
		turnsLog = append(turnsLog, TurnEntry{Role: "model", Text: remModel, Time: nowStr})
	}
	finalTurns := append([]TurnEntry(nil), turnsLog...)
	resHandle := latestResumptionHandle
	finalUsage := latestUsage
	logMu.Unlock()

	if len(finalTurns) > 0 {
		_ = saveSessionRecord(SessionRecord{
			ID:               sessionID,
			StartedAt:        startedAt,
			DurationSec:      time.Since(sessionStart).Seconds(),
			Model:            cfg.Model,
			Voice:            cfg.VoiceName,
			KeyName:          connectedKeyName,
			ResumptionHandle: resHandle,
			Usage:            finalUsage,
			Turns:            finalTurns,
		})
	}

	if cfg.AutoExtractMemory && len(finalTurns) >= 2 && connectedKeyVal != "" {
		go extractMemoriesAndSummary(connectedKeyVal, sessionID, finalTurns)
	}
}

// extractMemoriesAndSummary runs after a session ends using gemini-3.5-flash
// to extract durable facts AND update the rolling "Last Session Summary" memory item.
func extractMemoriesAndSummary(apiKey string, sessionID string, turns []TurnEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	var lines []string
	for _, t := range turns {
		if t.Role == "tool" {
			continue
		}
		role := "User"
		if t.Role == "model" {
			role = "Gemini"
		}
		lines = append(lines, fmt.Sprintf("%s: %s", role, t.Text))
	}
	if len(lines) == 0 {
		return
	}
	convoText := strings.Join(lines, "\n")

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return
	}

	prompt := fmt.Sprintf(`Analyze this voice conversation transcript between the User and Gemini.
Return a JSON object with two fields:
1. "summary": A concise 1-2 sentence summary of what was discussed or accomplished in this session.
2. "facts": A JSON array of new, durable personal facts, preferences, or project details about the User worth remembering long-term (each with "fact" and "category" in ["preference", "project", "user", "task"]). If no new durable facts were shared, return [].

Transcript:
%s`, convoText)

	temp := float32(0.1)
	resp, err := client.Models.GenerateContent(ctx, "gemini-3.8-flash", []*genai.Content{
		{
			Role:  "user",
			Parts: []*genai.Part{{Text: prompt}},
		},
	}, &genai.GenerateContentConfig{
		Temperature:      &temp,
		ResponseMIMEType: "application/json",
	})
	if err != nil {
		fmt.Printf("%s \033[33m[MEMORY WARNING]\033[0m Extraction error: %v\n", time.Now().Format("15:04:05"), err)
		return
	}
	if resp == nil {
		return
	}

	rawJSON := strings.TrimSpace(resp.Text())
	if rawJSON == "" {
		return
	}

	var parsed struct {
		Summary string `json:"summary"`
		Facts   []struct {
			Fact     string `json:"fact"`
			Category string `json:"category"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return
	}

	if strings.TrimSpace(parsed.Summary) != "" {
		addMemoryItem(
			fmt.Sprintf("Last session (%s): %s", sessionID, strings.TrimSpace(parsed.Summary)),
			"summary",
			fmt.Sprintf("auto:%s", sessionID),
			true,
		)
		fmt.Printf("%s \033[35m[MEMORY]\033[0m Updated rolling session summary: \"%s\"\n",
			time.Now().Format("15:04:05"), strings.TrimSpace(parsed.Summary))
	}
	for _, f := range parsed.Facts {
		if strings.TrimSpace(f.Fact) != "" {
			cat := f.Category
			if cat == "" {
				cat = "fact"
			}
			addMemoryItem(f.Fact, cat, fmt.Sprintf("auto:%s", sessionID), false)
			fmt.Printf("%s \033[35m[MEMORY]\033[0m Stored new durable fact [%s]: %s\n",
				time.Now().Format("15:04:05"), cat, f.Fact)
		}
	}
}
