package storage

import (
	"time"

	util "github.com/annelo/go-grpc-server/internal/storage/util"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// ChunkDelta представляет собой дельту (изменения) чанка
type ChunkDelta struct {
	ChunkPos     *game.ChunkPosition
	BaseVersion  int64
	BlockChanges map[uint16]*game.Block // Измененные и удаленные блоки
	CreatedAt    time.Time
	LastModified time.Time
	AccessTime   time.Time // Время последнего доступа для LRU
}

// NewChunkDelta создаёт новую дельту чанка
func NewChunkDelta(pos *game.ChunkPosition) *ChunkDelta {
	now := time.Now()
	return &ChunkDelta{
		ChunkPos:     pos,
		BaseVersion:  1,
		BlockChanges: make(map[uint16]*game.Block),
		CreatedAt:    now,
		LastModified: now,
		AccessTime:   now,
	}
}

// Добавление/изменение блока
func (delta *ChunkDelta) SetBlock(block *game.Block) bool {
	key := util.BlockKey(block.X, block.Y)

	// Если блок не изменился — ничего не делаем
	if existing, ok := delta.BlockChanges[key]; ok {
		if existing.Type == block.Type && compareProps(existing.Properties, block.Properties) {
			return false
		}
	}

	delta.BlockChanges[key] = block
	delta.LastModified = time.Now()
	return true
}

// Удаление блока (установка Air)
func (delta *ChunkDelta) RemoveBlock(x, y int32) bool {
	key := util.BlockKey(x, y)
	existing, ok := delta.BlockChanges[key]
	if ok && existing.Type == 0 {
		return false // уже удалён
	}
	delta.BlockChanges[key] = &game.Block{X: x, Y: y, Type: 0}
	delta.LastModified = time.Now()
	return true
}

// Проверка, изменен ли блок
func (delta *ChunkDelta) IsBlockModified(x, y int32) bool {
	key := util.BlockKey(x, y)
	_, exists := delta.BlockChanges[key]
	return exists
}

// Получение блока с учетом изменений
func (delta *ChunkDelta) GetBlock(x, y int32, baseChunk *game.Chunk) *game.Block {
	key := util.BlockKey(x, y)

	// Проверяем наличие изменений
	if block, exists := delta.BlockChanges[key]; exists {
		return block
	}

	// Иначе ищем блок в базовом чанке
	for _, block := range baseChunk.Blocks {
		if block.X == x && block.Y == y {
			return block
		}
	}

	// Не найден
	return nil
}

// Обновляет время последнего доступа
func (delta *ChunkDelta) Touch() {
	delta.AccessTime = time.Now()
}

// Применение дельты к базовому чанку
func ApplyDeltaToChunk(baseChunk *game.Chunk, delta *ChunkDelta) *game.Chunk {
	// Создаем копию базового чанка
	resultChunk := &game.Chunk{
		Position: baseChunk.Position,
		Entities: baseChunk.Entities, // Просто копируем сущности
		Blocks:   make([]*game.Block, 0, len(baseChunk.Blocks)),
	}

	// Копируем блоки, которые не были изменены
	for _, block := range baseChunk.Blocks {
		key := util.BlockKey(block.X, block.Y)
		if _, isModified := delta.BlockChanges[key]; !isModified {
			resultChunk.Blocks = append(resultChunk.Blocks, block)
		}
	}

	// Добавляем измененные блоки (кроме Air)
	for _, block := range delta.BlockChanges {
		if block.Type != 0 { // Не добавляем Air-блоки
			resultChunk.Blocks = append(resultChunk.Blocks, block)
		}
	}

	return resultChunk
}

// compareProps возвращает true, если две карты свойств равны
func compareProps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
