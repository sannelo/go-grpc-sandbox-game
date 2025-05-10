package util

import (
	"fmt"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// BlockKey формирует компактный uint16-ключ для блока (x << 8 | y).
// Предполагается, что x и y лежат в диапазоне 0..255. При других значениях
// старшие биты усекаются маской 0xFF для совместимости.
func BlockKey(x, y int32) uint16 {
	return uint16((x&0xFF)<<8 | (y & 0xFF))
}

// ChunkKey формирует уникальный ключ для позиции чанка.
func ChunkKey(pos *game.ChunkPosition) string {
	return fmt.Sprintf("%d:%d", pos.X, pos.Y)
}
