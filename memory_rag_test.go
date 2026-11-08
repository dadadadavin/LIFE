package main

import (
	"math"
	"os"
	"testing"
	"time"
)

func TestRAGCosineSimilarity(t *testing.T) {
	// 1. Identical vectors -> similarity = 1.0
	v1 := []float32{1, 0, 0}
	v2 := []float32{1, 0, 0}
	sim := cosineSimilarity(v1, v2)
	if math.Abs(sim-1.0) > 1e-6 {
		t.Errorf("Identical vectors cosineSimilarity = %v, want 1.0", sim)
	}

	// 2. Orthogonal vectors -> similarity = 0.0
	v3 := []float32{0, 1, 0}
	sim = cosineSimilarity(v1, v3)
	if math.Abs(sim-0.0) > 1e-6 {
		t.Errorf("Orthogonal vectors cosineSimilarity = %v, want 0.0", sim)
	}

	// 3. Opposite vectors -> similarity = -1.0
	v4 := []float32{-1, 0, 0}
	sim = cosineSimilarity(v1, v4)
	if math.Abs(sim-(-1.0)) > 1e-6 {
		t.Errorf("Opposite vectors cosineSimilarity = %v, want -1.0", sim)
	}

	// 4. Arbitrary vectors
	a := []float32{3, 4}
	b := []float32{4, 3}
	// dot = 12 + 12 = 24. normA = 5, normB = 5 -> 24 / 25 = 0.96
	sim = cosineSimilarity(a, b)
	if math.Abs(sim-0.96) > 1e-6 {
		t.Errorf("Arbitrary vectors cosineSimilarity = %v, want 0.96", sim)
	}
}

func TestRAGHybridScore(t *testing.T) {
	now := time.Now()

	// Brand new item: recency = 1.0, cosSim = 1.0 -> hybrid = 0.85*1.0 + 0.15*1.0 = 1.0
	rec, hyb := computeHybridScore(1.0, now)
	if math.Abs(rec-1.0) > 1e-5 || math.Abs(hyb-1.0) > 1e-5 {
		t.Errorf("Brand new item: recency=%v, hybrid=%v, want 1.0, 1.0", rec, hyb)
	}

	// Exactly 7 days old: recency = 0.5
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)
	rec, hyb = computeHybridScore(1.0, sevenDaysAgo)
	if math.Abs(rec-0.5) > 1e-3 {
		t.Errorf("7 days old item: recency=%v, want 0.5", rec)
	}
	expectedHyb := 0.85*1.0 + 0.15*0.5 // 0.85 + 0.075 = 0.925
	if math.Abs(hyb-expectedHyb) > 1e-3 {
		t.Errorf("7 days old item: hybrid=%v, want %v", hyb, expectedHyb)
	}
}

func TestRAGCRUDFileOperations(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lifel_rag_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldBaseDir := baseDir
	baseDir = tmpDir
	defer func() { baseDir = oldBaseDir }()

	// 1. Initial load on empty
	chunks := loadRAGChunks()
	if len(chunks) != 0 {
		t.Errorf("Expected 0 chunks, got %d", len(chunks))
	}

	// 2. Insert chunk
	c, err := InsertRAGChunk("Test Architecture", "technical", "Lifel is written in Go with HTMX.", "")
	if err != nil {
		t.Fatalf("InsertRAGChunk failed: %v", err)
	}
	if c.ID == "" || c.Title != "Test Architecture" {
		t.Errorf("Inserted chunk unexpected fields: %+v", c)
	}

	// 3. Verify saved
	chunks = loadRAGChunks()
	if len(chunks) != 1 {
		t.Fatalf("Expected 1 chunk after insert, got %d", len(chunks))
	}
	if chunks[0].ID != c.ID {
		t.Errorf("Chunk ID mismatch: %s vs %s", chunks[0].ID, c.ID)
	}

	// 4. Update chunk
	err = UpdateRAGChunk(c.ID, "Updated Architecture", "technical", "Updated content with RAG engine.", "")
	if err != nil {
		t.Fatalf("UpdateRAGChunk failed: %v", err)
	}
	chunks = loadRAGChunks()
	if chunks[0].Title != "Updated Architecture" || chunks[0].Content != "Updated content with RAG engine." {
		t.Errorf("Updated chunk fields mismatch: %+v", chunks[0])
	}

	// 5. Search
	scored, err := SearchRAGMemory("Architecture", 5, "")
	if err != nil {
		t.Fatalf("SearchRAGMemory failed: %v", err)
	}
	if len(scored) == 0 {
		t.Errorf("Expected at least 1 scored result, got 0")
	}

	// 6. Delete chunk
	err = DeleteRAGChunk(c.ID)
	if err != nil {
		t.Fatalf("DeleteRAGChunk failed: %v", err)
	}
	chunks = loadRAGChunks()
	if len(chunks) != 0 {
		t.Errorf("Expected 0 chunks after delete, got %d", len(chunks))
	}
}
