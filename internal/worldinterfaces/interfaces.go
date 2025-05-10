// Package worldinterfaces содержит общие интерфейсы для избегания циклических зависимостей
package worldinterfaces

import (
	"context"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// ChunkManagerInterface определяет интерфейс для ChunkManager, который используется в BlockManager
type ChunkManagerInterface interface {
	GetOrGenerateChunk(pos *game.ChunkPosition) (*game.Chunk, error)
	MarkChunkAsActive(pos *game.ChunkPosition)
	UnmarkChunkAsActive(pos *game.ChunkPosition)
	IsChunkActive(pos *game.ChunkPosition) bool
	SetBlock(pos *game.ChunkPosition, block *game.Block) error
}

// BlockManagerInterface определяет интерфейс для BlockManager, который используется в ChunkManager
type BlockManagerInterface interface {
	// CreateBlock пришлось заменить на обобщенный тип (interface{}) для избежания циклической зависимости
	// В реальной реализации это будет конкретный тип Block
	CreateBlock(x, y int32, chunkPos *game.ChunkPosition, blockType int32) (interface{}, error)
	LoadBlocksFromChunk(chunk *game.Chunk) error
	SaveBlocksToChunk(chunk *game.Chunk) error
	InteractWithBlock(playerID string, x, y int32, chunkPos *game.ChunkPosition, interactionType string, data map[string]string) (*game.WorldEvent, error)
	StartBlockUpdateLoop(ctx context.Context)
	GetBlockEvents() <-chan *game.WorldEvent
}
