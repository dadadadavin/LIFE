package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
)

// MemoryChunk represents a rich, multi-paragraph episodic, technical, or project
// knowledge block (500–1,500 tokens) backed by a 3,072-dimensional vector embedding.
type MemoryChunk struct {
	ID        string    `json:"id"`
	Category  string    `json:"category"` // "episodic", "technical", "preference", "project"
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Embedding []float32 `json:"embedding"`
}

// ScoredChunk pairs a retrieved MemoryChunk with its vector similarity and hybrid scores.
type ScoredChunk struct {
	Chunk            MemoryChunk `json:"chunk"`
	CosineSimilarity float64     `json:"cosine_similarity"`
	RecencyScore     float64     `json:"recency_score"`
	HybridScore      float64     `json:"hybrid_score"`
}

// MemoryCRUDOperation models an atomic knowledge-graph mutation produced by gemini-3.8-flash.
type MemoryCRUDOperation struct {
	Op           string   `json:"op"` // "INSERT", "UPDATE", "MERGE", "DELETE"
	TargetID     string   `json:"target_id,omitempty"`
	SecondaryIDs []string `json:"secondary_ids,omitempty"`
	Category     string   `json:"category"`
	Title        string   `json:"title"`
	Content      string   `json:"content"`
}

// MemoryCRUDResponse models the structured JSON schema returned by the Memory Controller.
type MemoryCRUDResponse struct {
	Summary    string                `json:"summary"`
	Operations []MemoryCRUDOperation `json:"operations"`
}

var (
	ragMu   sync.RWMutex
	ragFile = "memory_rag.json"
)

// generateUUID returns a standard formatted UUID v4 string.
func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant RFC4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func getRAGFilePath() string {
	if baseDir != "" {
		return filepath.Join(baseDir, ragFile)
	}
	return ragFile
}

// loadRAGChunks reads all memory chunks from disk under a read lock.
func loadRAGChunks() []MemoryChunk {
	ragMu.RLock()
	defer ragMu.RUnlock()

	p := getRAGFilePath()
	data, err := os.ReadFile(p)
	if err != nil {
		return []MemoryChunk{}
	}
	var chunks []MemoryChunk
	if err := json.Unmarshal(data, &chunks); err != nil {
		return []MemoryChunk{}
	}
	return chunks
}

// saveRAGChunks writes memory chunks atomically to disk under a write lock.
func saveRAGChunks(chunks []MemoryChunk) error {
	p := getRAGFilePath()
	data, err := json.MarshalIndent(chunks, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", p, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// embedText generates a normalized 3,072-dimensional vector embedding using gemini-embedding-2.
// It iterates over all available API keys with automatic failover and falls back to text-embedding-004 if needed.
func embedText(ctx context.Context, text, preferredKey string) ([]float32, error) {
	cleanText := strings.TrimSpace(text)
	if cleanText == "" {
		return nil, fmt.Errorf("cannot embed empty text")
	}

	keys := getOrderedAPIKeys(preferredKey, true)
	if len(keys) == 0 {
		return nil, fmt.Errorf("no Gemini API keys available for embedding")
	}

	models := []string{"gemini-embedding-2", "text-embedding-004"}
	var lastErr error

	for _, k := range keys {
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  k.Value,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			lastErr = err
			continue
		}

		for _, model := range models {
			reqCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			resp, err := client.Models.EmbedContent(reqCtx, model, []*genai.Content{
				{
					Parts: []*genai.Part{{Text: cleanText}},
				},
			}, nil)
			cancel()

			if err == nil && resp != nil && len(resp.Embeddings) > 0 {
				values := resp.Embeddings[0].Values
				if len(values) > 0 {
					return values, nil
				}
			}

			lastErr = err
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}

			// If rate limited or quota exceeded on this key, try next key immediately
			if strings.Contains(errStr, "429") || strings.Contains(errStr, "RESOURCE_EXHAUSTED") || strings.Contains(errStr, "quota") {
				break
			}
		}
	}

	return nil, fmt.Errorf("embedText failed across all available keys: %w", lastErr)
}

// cosineSimilarity computes the normalized dot product between two float32 vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := 0; i < len(a); i++ {
		valA := float64(a[i])
		valB := float64(b[i])
		dot += valA * valB
		normA += valA * valA
		normB += valB * valB
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// computeHybridScore calculates relevance as 0.85 * CosineSimilarity + 0.15 * RecencyWeight.
// Recency weight applies a 7-day half-life: 0.5^(age / 7 days).
func computeHybridScore(cosSim float64, updatedAt time.Time) (recency float64, hybrid float64) {
	age := time.Since(updatedAt)
	if age < 0 {
		age = 0
	}
	const sevenDays = 7 * 24 * time.Hour
	recency = math.Pow(0.5, float64(age)/float64(sevenDays))
	hybrid = (0.85 * cosSim) + (0.15 * recency)
	return recency, hybrid
}

// SearchRAGMemory performs semantic search over the local RAG store, returning topK chunks
// ranked by hybrid score (0.85 cosine similarity + 0.15 7-day recency decay).
func SearchRAGMemory(query string, topK int, preferredKey string) ([]ScoredChunk, error) {
	chunks := loadRAGChunks()
	if len(chunks) == 0 {
		return nil, nil
	}
	if topK <= 0 {
		topK = 3
	}

	cleanQuery := strings.TrimSpace(query)
	var queryEmbedding []float32
	if cleanQuery != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		emb, err := embedText(ctx, cleanQuery, preferredKey)
		cancel()
		if err == nil {
			queryEmbedding = emb
		}
	}

	scored := make([]ScoredChunk, 0, len(chunks))
	for _, ch := range chunks {
		var cosSim float64
		if len(queryEmbedding) > 0 && len(ch.Embedding) == len(queryEmbedding) {
			cosSim = cosineSimilarity(queryEmbedding, ch.Embedding)
		} else if cleanQuery != "" {
			// Fallback keyword overlap if vector comparison is unavailable
			lowerQ := strings.ToLower(cleanQuery)
			lowerC := strings.ToLower(ch.Content + " " + ch.Title)
			if strings.Contains(lowerC, lowerQ) {
				cosSim = 0.75
			}
		} else {
			// Empty query: recency baseline
			cosSim = 0.5
		}

		refTime := ch.UpdatedAt
		if refTime.IsZero() {
			refTime = ch.CreatedAt
		}
		rec, hyb := computeHybridScore(cosSim, refTime)
		scored = append(scored, ScoredChunk{
			Chunk:            ch,
			CosineSimilarity: cosSim,
			RecencyScore:     rec,
			HybridScore:      hyb,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].HybridScore > scored[j].HybridScore
	})

	if len(scored) > topK {
		scored = scored[:topK]
	}
	return scored, nil
}

// InsertRAGChunk embeds and commits a new memory chunk to disk.
func InsertRAGChunk(title, category, content, preferredKey string) (*MemoryChunk, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Memory Entry"
	}
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		category = "episodic"
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("chunk content cannot be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	emb, err := embedText(ctx, title+"\n"+content, preferredKey)
	cancel()
	if err != nil {
		fmt.Printf("%s \033[33m[RAG WARNING]\033[0m Embedding failed (%v), saving chunk with empty vector\n",
			time.Now().Format("15:04:05"), err)
	}

	now := time.Now()
	chunk := MemoryChunk{
		ID:        generateUUID(),
		Category:  category,
		Title:     title,
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
		Embedding: emb,
	}

	ragMu.Lock()
	defer ragMu.Unlock()

	p := getRAGFilePath()
	var chunks []MemoryChunk
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &chunks)
	}
	chunks = append(chunks, chunk)

	data, err := json.MarshalIndent(chunks, "", "  ")
	if err != nil {
		return nil, err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", p, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return nil, err
	}

	return &chunk, nil
}

// UpdateRAGChunk updates an existing chunk by UUID and refreshes its embedding.
func UpdateRAGChunk(id, title, category, content, preferredKey string) error {
	ragMu.Lock()
	defer ragMu.Unlock()

	p := getRAGFilePath()
	var chunks []MemoryChunk
	data, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("memory store not found: %w", err)
	}
	if err := json.Unmarshal(data, &chunks); err != nil {
		return err
	}

	foundIdx := -1
	for i, c := range chunks {
		if c.ID == id {
			foundIdx = i
			break
		}
	}
	if foundIdx == -1 {
		return fmt.Errorf("memory chunk with ID %q not found", id)
	}

	if title != "" {
		chunks[foundIdx].Title = title
	}
	if category != "" {
		chunks[foundIdx].Category = category
	}
	if content != "" {
		chunks[foundIdx].Content = content
	}
	chunks[foundIdx].UpdatedAt = time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	emb, err := embedText(ctx, chunks[foundIdx].Title+"\n"+chunks[foundIdx].Content, preferredKey)
	cancel()
	if err == nil && len(emb) > 0 {
		chunks[foundIdx].Embedding = emb
	}

	marshalled, err := json.MarshalIndent(chunks, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", p, time.Now().UnixNano())
	if err := os.WriteFile(tmp, marshalled, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// DeleteRAGChunk removes a memory chunk by UUID.
func DeleteRAGChunk(id string) error {
	ragMu.Lock()
	defer ragMu.Unlock()

	p := getRAGFilePath()
	var chunks []MemoryChunk
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	if err := json.Unmarshal(data, &chunks); err != nil {
		return err
	}

	var filtered []MemoryChunk
	for _, c := range chunks {
		if c.ID != id {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == len(chunks) {
		return nil
	}

	marshalled, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", p, time.Now().UnixNano())
	if err := os.WriteFile(tmp, marshalled, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// MergeRAGChunks enriches targetID with newContent and removes secondaryIDs.
func MergeRAGChunks(targetID string, secondaryIDs []string, newTitle, newCategory, newContent, preferredKey string) error {
	ragMu.Lock()
	defer ragMu.Unlock()

	p := getRAGFilePath()
	var chunks []MemoryChunk
	data, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("memory store not found: %w", err)
	}
	if err := json.Unmarshal(data, &chunks); err != nil {
		return err
	}

	secMap := make(map[string]bool)
	for _, sid := range secondaryIDs {
		secMap[sid] = true
	}

	targetIdx := -1
	for i, c := range chunks {
		if c.ID == targetID {
			targetIdx = i
			break
		}
	}

	if targetIdx == -1 {
		return fmt.Errorf("target chunk %q not found for merge", targetID)
	}

	if newTitle != "" {
		chunks[targetIdx].Title = newTitle
	}
	if newCategory != "" {
		chunks[targetIdx].Category = newCategory
	}
	if newContent != "" {
		chunks[targetIdx].Content = newContent
	}
	chunks[targetIdx].UpdatedAt = time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	emb, err := embedText(ctx, chunks[targetIdx].Title+"\n"+chunks[targetIdx].Content, preferredKey)
	cancel()
	if err == nil && len(emb) > 0 {
		chunks[targetIdx].Embedding = emb
	}

	var filtered []MemoryChunk
	for _, c := range chunks {
		if secMap[c.ID] {
			continue
		}
		filtered = append(filtered, c)
	}

	marshalled, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", p, time.Now().UnixNano())
	if err := os.WriteFile(tmp, marshalled, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// getPinnedAndProjectIndex retrieves pinned identity facts and active project names
// for Layer 4 of the system instruction, guaranteeing the payload remains under 400 tokens.
func getPinnedAndProjectIndex() ([]string, []string) {
	// 1. Pinned items from memory.json
	var pinned []string
	allMemories := loadMemories()
	for _, m := range allMemories {
		if m.Pinned && m.Category != "summary" {
			clean := strings.TrimSpace(m.Content)
			if clean != "" {
				pinned = append(pinned, fmt.Sprintf("[%s] %s", strings.ToUpper(m.Category), clean))
			}
		}
	}
	if len(pinned) > 5 {
		pinned = pinned[:5]
	}

	// 2. Project index from memory_rag.json
	chunks := loadRAGChunks()
	projSet := make(map[string]bool)
	var projects []string
	for _, c := range chunks {
		if c.Category == "project" || strings.Contains(strings.ToLower(c.Title), "project") {
			name := strings.TrimSpace(c.Title)
			if name != "" && !projSet[name] {
				projSet[name] = true
				projects = append(projects, name)
			}
		}
	}
	sort.Strings(projects)
	if len(projects) > 8 {
		projects = projects[:8]
	}

	return pinned, projects
}

// consolidateTranscriptBatch is the autonomous Memory CRUD Controller driven by gemini-3.8-flash.
// It embeds the incoming batch of turns, queries the top-10 semantically related existing chunks,
// and instructs Flash to output atomic INSERT, UPDATE, MERGE, and DELETE operations.
func consolidateTranscriptBatch(sessionID string, batch []TurnEntry, preferredKey string) {
	if len(batch) == 0 {
		return
	}

	var lines []string
	for _, t := range batch {
		if strings.TrimSpace(t.Text) == "" {
			continue
		}
		role := t.Role
		switch role {
		case "user":
			role = "User"
		case "model":
			role = "Lifel"
		case "tool":
			role = "Tool"
		}
		lines = append(lines, fmt.Sprintf("%s: %s", role, t.Text))
	}
	if len(lines) == 0 {
		return
	}
	convoText := strings.Join(lines, "\n")
	if len(convoText) < 25 {
		return
	}

	// 1. Retrieve top 10 semantically related existing chunks from RAG store
	existingChunks, _ := SearchRAGMemory(convoText, 10, preferredKey)
	var existingBlocks []string
	for _, sc := range existingChunks {
		c := sc.Chunk
		existingBlocks = append(existingBlocks, fmt.Sprintf(
			"[MEMORY ID: %s] | Title: %s | Category: %s | Updated: %s\n%s",
			c.ID, c.Title, c.Category, c.UpdatedAt.Format("2006-01-02"), c.Content,
		))
	}
	existingStr := "No existing memory chunks found. This is a fresh index."
	if len(existingBlocks) > 0 {
		existingStr = strings.Join(existingBlocks, "\n---\n")
	}

	prompt := fmt.Sprintf(`You are the Autonomous Memory CRUD Controller for Lifel, an advanced personal AI operating assistant for macOS.
Your mission is to maintain a deep, rich, non-redundant local semantic knowledge graph of the user.

=== EXISTING CANDIDATE MEMORY CHUNKS (Top-10 Semantic Matches) ===
%s

=== NEW CONVERSATION TRANSCRIPT BATCH (Session %s) ===
%s

=== CURATION DIRECTIVES ===
Analyze the transcript batch against existing candidate chunks and output an array of atomic operations:
1. "INSERT": Add a distinct new multi-paragraph knowledge block (500-1500 tokens of rich context) for new technical setups, workflows, preferences, or episodic milestones.
   - Leave target_id empty or omit it.
2. "UPDATE": Amend, expand, or correct an existing chunk when new details refine it.
   - Specify target_id with the existing memory chunk UUID.
3. "MERGE": Combine related fragments or consolidate new information into an existing chunk.
   - Specify target_id with the primary UUID to keep/expand.
   - List any secondary_ids to be pruned and absorbed.
4. "DELETE": Purge an existing chunk if its facts were explicitly contradicted, superseded, or instructed to be forgotten.
   - Specify target_id with the UUID to delete.
5. If the transcript contains only fleeting chit-chat, greetings, or ephemeral status with zero durable knowledge, output an empty operations array.
6. Provide a crisp 1-2 sentence rolling session summary in "summary".

Strictly return valid JSON conforming to this schema:
{
  "summary": "1-2 sentence rolling summary of the conversation batch...",
  "operations": [
    {
      "op": "INSERT" | "UPDATE" | "MERGE" | "DELETE",
      "target_id": "UUID (required for UPDATE, MERGE, DELETE)",
      "secondary_ids": ["UUIDs to delete for MERGE"],
      "category": "episodic" | "technical" | "preference" | "project",
      "title": "Descriptive Title",
      "content": "Rich, multi-paragraph factual detail..."
    }
  ]
}`, existingStr, sessionID, convoText)

	keys := getOrderedAPIKeys(preferredKey, true)
	if len(keys) == 0 {
		return
	}

	models := []string{
		"gemini-3.5-flash-lite", // Primary: fast & verified working in ~3s
		"gemini-3.6-flash",      // Secondary: verified working
		"gemini-flash-lite-latest",
	}

	tStart := time.Now()
	fmt.Printf("%s \033[35m[RAG CRUD]\033[0m Starting memory consolidation for %d turns (Session: %s)...\n",
		tStart.Format("15:04:05"), len(batch), sessionID)

	var rawJSON string
	var successfulModel string
	var successfulKey string
	var lastErr error

KeyLoop:
	for _, k := range keys {
		ctx := context.Background()
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  k.Value,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			lastErr = err
			continue
		}

		for _, modelName := range models {
			for attempt := 0; attempt < 2; attempt++ {
				if attempt > 0 {
					time.Sleep(time.Duration(attempt) * 750 * time.Millisecond)
				}
				fmt.Printf("%s \033[35m[RAG CRUD]\033[0m Calling %s via key %s (attempt %d/2)...\n",
					time.Now().Format("15:04:05"), modelName, k.Name, attempt+1)
				reqCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				temp := float32(0.1)
				resp, err := client.Models.GenerateContent(reqCtx, modelName, []*genai.Content{
					{
						Role:  "user",
						Parts: []*genai.Part{{Text: prompt}},
					},
				}, &genai.GenerateContentConfig{
					Temperature:      &temp,
					ResponseMIMEType: "application/json",
				})
				cancel()

				if err == nil && resp != nil {
					text := strings.TrimSpace(resp.Text())
					if text != "" {
						rawJSON = text
						successfulModel = modelName
						successfulKey = k.Name
						fmt.Printf("%s \033[32m[RAG CRUD SUCCESS]\033[0m Distilled %d turns in %v via %s (Key: %s)\n",
							time.Now().Format("15:04:05"), len(batch), time.Since(tStart).Truncate(10*time.Millisecond), modelName, k.Name)
						break KeyLoop
					}
				}

				lastErr = err
				errStr := ""
				if err != nil {
					errStr = err.Error()
				}
				fmt.Printf("%s \033[33m[RAG CRUD ATTEMPT FAILED]\033[0m %s via %s: %s\n",
					time.Now().Format("15:04:05"), modelName, k.Name, errStr)

				if strings.Contains(errStr, "503") || strings.Contains(errStr, "overloaded") || strings.Contains(errStr, "UNAVAILABLE") {
					break
				}
				if strings.Contains(errStr, "429") || strings.Contains(errStr, "RESOURCE_EXHAUSTED") || strings.Contains(errStr, "quota") {
					continue KeyLoop
				}
			}
		}
	}

	if rawJSON == "" {
		fmt.Printf("%s \033[33m[RAG CRUD WARNING]\033[0m Distillation failed across keys (%v)\n",
			time.Now().Format("15:04:05"), lastErr)
		return
	}

	fmt.Printf("%s \033[32m[RAG CRUD CONTROLLER]\033[0m Processing response from %s (Key: %s)\n",
		time.Now().Format("15:04:05"), successfulModel, successfulKey)

	var parsed MemoryCRUDResponse
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		fmt.Printf("%s \033[33m[RAG CRUD WARNING]\033[0m Invalid JSON from model: %v\n",
			time.Now().Format("15:04:05"), err)
		return
	}

	// Update rolling session summary for UI visibility
	if strings.TrimSpace(parsed.Summary) != "" && sessionID != "" {
		addMemoryItem(
			fmt.Sprintf("Last session (%s): %s", sessionID, strings.TrimSpace(parsed.Summary)),
			"summary",
			fmt.Sprintf("auto:%s", sessionID),
			true,
		)
	}

	// Execute atomic operations
	for _, op := range parsed.Operations {
		action := strings.ToUpper(strings.TrimSpace(op.Op))
		switch action {
		case "INSERT":
			if strings.TrimSpace(op.Content) != "" {
				ch, err := InsertRAGChunk(op.Title, op.Category, op.Content, preferredKey)
				if err == nil && ch != nil {
					fmt.Printf("%s \033[32m[RAG INSERT]\033[0m Added chunk %s (%s: %q)\n",
						time.Now().Format("15:04:05"), ch.ID, ch.Category, ch.Title)
				}
			}
		case "UPDATE":
			if strings.TrimSpace(op.TargetID) != "" {
				err := UpdateRAGChunk(op.TargetID, op.Title, op.Category, op.Content, preferredKey)
				if err == nil {
					fmt.Printf("%s \033[34m[RAG UPDATE]\033[0m Updated chunk %s (%q)\n",
						time.Now().Format("15:04:05"), op.TargetID, op.Title)
				}
			}
		case "MERGE":
			if strings.TrimSpace(op.TargetID) != "" {
				err := MergeRAGChunks(op.TargetID, op.SecondaryIDs, op.Title, op.Category, op.Content, preferredKey)
				if err == nil {
					fmt.Printf("%s \033[34m[RAG MERGE]\033[0m Merged into chunk %s (pruned %v)\n",
						time.Now().Format("15:04:05"), op.TargetID, op.SecondaryIDs)
				}
			}
		case "DELETE":
			if strings.TrimSpace(op.TargetID) != "" {
				err := DeleteRAGChunk(op.TargetID)
				if err == nil {
					fmt.Printf("%s \033[31m[RAG DELETE]\033[0m Deleted chunk %s\n",
						time.Now().Format("15:04:05"), op.TargetID)
				}
			}
		}
	}
}
