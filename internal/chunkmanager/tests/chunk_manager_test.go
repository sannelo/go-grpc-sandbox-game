package chunkmanager_test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

func TestChunkManager_GenerateAndCache(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	cm := chunkmanager.NewChunkManager(rnd)

	pos := &game.ChunkPosition{X: 0, Y: 0}

	chunk1, err := cm.GetOrGenerateChunk(pos)
	if err != nil {
		t.Fatalf("first generate returned error: %v", err)
	}
	if len(chunk1.Blocks) == 0 {
		t.Fatalf("generated chunk has no blocks")
	}

	// Request again, should hit cache
	chunk2, err := cm.GetOrGenerateChunk(pos)
	if err != nil {
		t.Fatalf("second get returned error: %v", err)
	}
	if chunk1 != chunk2 {
		t.Fatalf("expected same chunk instance from cache")
	}

	// Test SetBlock
	block := &game.Block{X: 1, Y: 1, Type: 2}
	if err := cm.SetBlock(pos, block); err != nil {
		t.Fatalf("SetBlock error: %v", err)
	}

	// Ensure block present
	got, err := cm.GetChunk(pos)
	if err != nil {
		t.Fatalf("GetChunk error: %v", err)
	}
	found := false
	for _, b := range got.Blocks {
		if b.X == block.X && b.Y == block.Y && b.Type == block.Type {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("block not found after SetBlock")
	}
}

func TestChunkManager_CacheStats(t *testing.T) {
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	cm := chunkmanager.NewChunkManager(rnd)

	// Access biome data multiple times same coordinates to generate hits
	for i := 0; i < 5; i++ {
		cm.GetBlockTypeFromBiomeData(10.0, 20.0)
	}

	stats := cm.GetCacheStats()
	bc := stats["block_cache"].(map[string]interface{})
	if bc["hits"].(int) == 0 {
		t.Fatalf("expected some cache hits")
	}
}
