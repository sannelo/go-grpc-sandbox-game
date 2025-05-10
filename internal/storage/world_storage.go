package storage

import (
	"context"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// WorldStorage представляет интерфейс для хранения данных игрового мира
type WorldStorage interface {
	// SaveChunk сохраняет чанк в хранилище
	SaveChunk(ctx context.Context, chunk *game.Chunk) error

	// LoadChunk загружает чанк из хранилища
	// Возвращает ошибку типа ErrChunkNotFound, если чанк не найден
	LoadChunk(ctx context.Context, position *game.ChunkPosition) (*game.Chunk, error)

	// DeleteChunk удаляет чанк из хранилища
	DeleteChunk(ctx context.Context, position *game.ChunkPosition) error

	// ListChunks возвращает список всех сохранённых чанков
	ListChunks(ctx context.Context) ([]*game.ChunkPosition, error)

	// SaveWorld сохраняет общую информацию о мире
	SaveWorld(ctx context.Context, info *WorldInfo) error

	// LoadWorld загружает общую информацию о мире
	LoadWorld(ctx context.Context) (*WorldInfo, error)

	// Close закрывает хранилище и освобождает ресурсы
	Close() error

	// SavePlayerState сохраняет состояние игрока
	SavePlayerState(ctx context.Context, state *PlayerState) error

	// LoadPlayerState загружает состояние игрока, если существует
	LoadPlayerState(ctx context.Context, id string) (*PlayerState, error)
}

// WorldInfo содержит общую информацию о игровом мире
type WorldInfo struct {
	Name       string            // Название мира
	Seed       int64             // Сид для генерации
	Version    string            // Версия формата сохранения
	CreatedAt  int64             // Время создания (Unix timestamp)
	LastSaveAt int64             // Время последнего сохранения (Unix timestamp)
	Properties map[string]string // Дополнительные свойства
}

// ErrChunkNotFound возвращается, когда чанк не найден в хранилище
type ErrChunkNotFound struct {
	X int32
	Y int32
}

func (e ErrChunkNotFound) Error() string {
	return "чанк не найден в хранилище"
}

// PlayerState описывает сохраняемое состояние игрока
type PlayerState struct {
	ID       string         // Уникальный идентификатор игрока
	Name     string         // Имя игрока
	Position *game.Position // Позиция в мире
	Health   int32          // Текущее здоровье
	LastSeen int64          // Последнее время выхода (Unix)
}
