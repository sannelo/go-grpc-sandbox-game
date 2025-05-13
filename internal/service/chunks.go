package service

import (
	"fmt"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// loadChunkWithBlocks loads a chunk and its blocks into the BlockManager.
func (s *WorldService) loadChunkWithBlocks(pos *game.ChunkPosition) (*game.Chunk, error) {
	chunk, err := s.chunkManager.GetOrGenerateChunk(pos)
	if err != nil {
		return nil, err
	}
	// Load blocks into BlockManager
	if err := s.blockManager.LoadBlocksFromChunk(chunk); err != nil {
		s.logger.Warnf("Error loading blocks from chunk %s: %v", getChunkKey(pos), err)
	}
	// If chunk has active blocks, mark it active in ChunkManager
	if s.blockManager.GetChunkActiveStatus(pos) {
		s.chunkManager.MarkChunkAsActive(pos)
	}
	return chunk, nil
}

// getChunkKey returns a string key for the chunk position.
func getChunkKey(pos *game.ChunkPosition) string {
	return fmt.Sprintf("%d:%d", pos.X, pos.Y)
}
