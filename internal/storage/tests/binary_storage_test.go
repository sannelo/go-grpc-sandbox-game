package storage_test

import (
	"context"
	"testing"

	"github.com/annelo/go-grpc-server/internal/storage"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// TestBinaryStorage_SaveLoad проверяет базовый цикл сохранения/загрузки чанка.
func TestBinaryStorage_SaveLoad(t *testing.T) {
	tmpDir := t.TempDir()

	bs, err := storage.NewBinaryStorage(tmpDir, "world1", 123)
	if err != nil {
		t.Fatalf("unable to create binary storage: %v", err)
	}
	defer bs.Close()

	pos := &game.ChunkPosition{X: 0, Y: 0}

	// Создаём чанк с одним блоком
	chunk := &game.Chunk{
		Position: pos,
		Blocks: []*game.Block{
			{X: 1, Y: 2, Type: 3},
		},
		Entities: nil,
	}

	if err := bs.SaveChunk(context.Background(), chunk); err != nil {
		t.Fatalf("save chunk failed: %v", err)
	}

	loaded, err := bs.LoadChunk(context.Background(), pos)
	if err != nil {
		t.Fatalf("load chunk failed: %v", err)
	}

	if len(loaded.Blocks) != len(chunk.Blocks) {
		t.Fatalf("block count mismatch: want %d, got %d", len(chunk.Blocks), len(loaded.Blocks))
	}

	if loaded.Blocks[0].Type != chunk.Blocks[0].Type {
		t.Fatalf("block type mismatch: want %d, got %d", chunk.Blocks[0].Type, loaded.Blocks[0].Type)
	}
}
