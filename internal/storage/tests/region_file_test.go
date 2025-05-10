package storage_test

import (
	"testing"

	"github.com/annelo/go-grpc-server/internal/storage"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// TestRegionFile_SaveLoad проверяет, что после сохранения чанка его можно корректно загрузить обратно.
func TestRegionFile_SaveLoad(t *testing.T) {
	tmpDir := t.TempDir()

	// Создаём файл региона (0,0)
	region, err := storage.NewRegionFile(tmpDir, 0, 0)
	if err != nil {
		t.Fatalf("cannot create region file: %v", err)
	}
	defer region.Close()

	pos := &game.ChunkPosition{X: 1, Y: 2}

	delta := storage.NewChunkDelta(pos)
	b1 := &game.Block{X: 0, Y: 0, Type: 1}
	b2 := &game.Block{X: 5, Y: 10, Type: 2, Properties: map[string]string{"color": "red"}}

	delta.SetBlock(b1)
	delta.SetBlock(b2)

	if err := region.SaveChunk(delta); err != nil {
		t.Fatalf("save chunk delta failed: %v", err)
	}

	// Закрываем и открываем заново
	if err := region.Close(); err != nil {
		t.Fatalf("close region failed: %v", err)
	}

	reopened, err := storage.NewRegionFile(tmpDir, 0, 0)
	if err != nil {
		t.Fatalf("reopen region failed: %v", err)
	}
	defer reopened.Close()

	loadedDelta, err := reopened.GetChunk(pos)
	if err != nil {
		t.Fatalf("get chunk failed: %v", err)
	}

	if len(loadedDelta.BlockChanges) != len(delta.BlockChanges) {
		t.Fatalf("blockChanges length mismatch: want %d, got %d", len(delta.BlockChanges), len(loadedDelta.BlockChanges))
	}
}

// TestRegionFile_ChunkNotFound проверяет корректную ошибку при попытке загрузить несуществующий чанк.
func TestRegionFile_ChunkNotFound(t *testing.T) {
	dir := t.TempDir()

	region, err := storage.NewRegionFile(dir, 0, 0)
	if err != nil {
		t.Fatalf("cannot create region file: %v", err)
	}
	defer region.Close()

	_, err = region.GetChunk(&game.ChunkPosition{X: 100, Y: 200})
	if err == nil {
		t.Fatalf("expected error when loading non-existing chunk, got nil")
	}
}
