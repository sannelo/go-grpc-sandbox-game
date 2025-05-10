// Package world отвечает за инициализацию и связывание компонентов игрового мира
package world

import (
	"context"
	"math/rand"

	"github.com/annelo/go-grpc-server/internal/block"
	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/storage"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// World представляет полный игровой мир
type World struct {
	ChunkManager *chunkmanager.ChunkManager
	BlockManager *block.BlockManager
}

// NewWorld создает новый игровой мир с интегрированной системой Smart Blocks
func NewWorld(rnd *rand.Rand, storage storage.WorldStorage) *World {
	// Создаем менеджер чанков
	chunkManager := chunkmanager.NewChunkManagerWithStorage(rnd, storage)

	// Создаем менеджер блоков
	blockManager := block.NewBlockManager(chunkManager)

	// Устанавливаем ссылку на менеджер блоков в менеджере чанков
	// Необходимо использовать интерфейс для избежания циклической зависимости
	chunkManager.SetBlockManager(blockManagerWrapper{blockManager})

	// Регистрируем динамические и интерактивные блоки
	block.RegisterDynamicBlocks(blockManager)
	block.RegisterInteractiveBlocks(blockManager)

	return &World{
		ChunkManager: chunkManager,
		BlockManager: blockManager,
	}
}

// blockManagerWrapper оборачивает BlockManager для соответствия интерфейсу
type blockManagerWrapper struct {
	*block.BlockManager
}

// CreateBlock адаптирует возвращаемый тип для соответствия интерфейсу
func (w blockManagerWrapper) CreateBlock(x, y int32, chunkPos *game.ChunkPosition, blockType int32) (interface{}, error) {
	return w.BlockManager.CreateBlock(x, y, chunkPos, blockType)
}

// Start запускает все необходимые обработчики и цикы обновлений
func (w *World) Start(ctx context.Context) {
	// Загружаем чанки из хранилища
	w.ChunkManager.LoadChunksFromStorage(ctx)

	// Запускаем цикл обновления блоков
	w.BlockManager.StartBlockUpdateLoop(ctx)

	// Запускаем периодическое сохранение чанков
	w.ChunkManager.StartPeriodicalSaving(ctx, 5*60) // Сохраняем каждые 5 минут

	// Запускаем очистку кеша
	w.ChunkManager.StartCacheCleanupRoutine(ctx, 30) // Очищаем кеш каждые 30 секунд
}

// Stop останавливает все обработчики и сохраняет состояние мира
func (w *World) Stop(ctx context.Context) error {
	// Сохраняем все чанки перед выключением
	return w.ChunkManager.SaveChunksToStorage(ctx)
}

// GetEvents возвращает канал с игровыми событиями
func (w *World) GetEvents() <-chan *game.WorldEvent {
	// Возвращаем события блоков
	return w.BlockManager.GetBlockEvents()
}
