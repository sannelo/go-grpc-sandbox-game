package storage_test

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/annelo/go-grpc-server/internal/block"
	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/storage"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Адаптер для ChunkManager, реализующий только необходимые для тестов методы
type TestChunkManagerAdapter struct {
	ChunkManager *chunkmanager.ChunkManager
}

func (t *TestChunkManagerAdapter) GetOrGenerateChunk(pos *game.ChunkPosition) (*game.Chunk, error) {
	return t.ChunkManager.GetOrGenerateChunk(pos)
}

func (t *TestChunkManagerAdapter) MarkChunkAsActive(pos *game.ChunkPosition) {
	t.ChunkManager.MarkChunkAsActive(pos)
}

func (t *TestChunkManagerAdapter) UnmarkChunkAsActive(pos *game.ChunkPosition) {
	t.ChunkManager.UnmarkChunkAsActive(pos)
}

func (t *TestChunkManagerAdapter) IsChunkActive(pos *game.ChunkPosition) bool {
	return t.ChunkManager.IsChunkActive(pos)
}

// Конфигурация хранилища
type TestStorageConfig struct {
	WorldPath string
	WorldName string
	WorldSeed int64
}

// TestBlockStorageIntegration тестирует интеграцию системы блоков с хранилищем
func TestBlockStorageIntegration(t *testing.T) {
	// Создаем временную директорию для хранилища
	tempDir, err := os.MkdirTemp("", "block_storage_test")
	require.NoError(t, err, "Создание временной директории не должно вызывать ошибку")
	defer os.RemoveAll(tempDir)

	// Создаем конфигурацию хранилища
	storageConfig := TestStorageConfig{
		WorldPath: filepath.Join(tempDir, "world"),
		WorldName: "test_world",
		WorldSeed: 1234567890,
	}

	// Создаем хранилище
	worldStorage, err := storage.NewBinaryStorage(storageConfig.WorldPath, storageConfig.WorldName, storageConfig.WorldSeed)
	require.NoError(t, err, "Создание хранилища не должно вызывать ошибку")
	defer worldStorage.Close()

	// Создаем ChunkManager и BlockManager
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	chunkMgr := chunkmanager.NewChunkManagerWithStorage(rnd, worldStorage)
	chunkAdapter := &TestChunkManagerAdapter{ChunkManager: chunkMgr}
	blockMgr := block.NewBlockManager(chunkAdapter)

	// Регистрируем фабрики блоков
	block.RegisterDynamicBlocks(blockMgr)
	block.RegisterInteractiveBlocks(blockMgr)

	// Создаем тестовые блоки
	chunkPos := &game.ChunkPosition{X: 1, Y: 2}

	// Статический блок
	stoneBlock, err := blockMgr.CreateBlock(1, 1, chunkPos, chunkmanager.BlockTypeStone)
	require.NoError(t, err, "Создание статического блока не должно вызывать ошибку")
	assert.NotNil(t, stoneBlock, "Статический блок должен быть создан")

	// Динамический блок
	fireBlock, err := blockMgr.CreateBlock(2, 2, chunkPos, block.BlockTypeFire)
	require.NoError(t, err, "Создание блока огня не должно вызывать ошибку")

	// Интерактивный блок
	doorBlock, err := blockMgr.CreateBlock(3, 3, chunkPos, block.BlockTypeDoor)
	require.NoError(t, err, "Создание блока двери не должно вызывать ошибку")

	// Изменяем свойства блоков
	// Огонь
	fireBlock.(*block.FireBlock).BaseBlock.SetProperty(block.FirePropertyIntensity, "5")

	// Дверь
	doorInteractive := doorBlock.(block.InteractiveBlock)
	_, err = doorInteractive.OnInteract("player1", block.InteractionTypeUse, nil)
	require.NoError(t, err, "Взаимодействие с дверью не должно вызывать ошибку")

	// Получаем чанк и сохраняем блоки в него
	chunk, err := chunkMgr.GetOrGenerateChunk(chunkPos)
	require.NoError(t, err, "Получение чанка не должно вызывать ошибку")

	err = blockMgr.SaveBlocksToChunk(chunk)
	require.NoError(t, err, "Сохранение блоков в чанк не должно вызывать ошибку")

	// Сохраняем чанк в хранилище
	err = worldStorage.SaveChunk(context.Background(), chunk)
	require.NoError(t, err, "Сохранение чанка в хранилище не должно вызывать ошибку")

	// Очищаем кеш менеджера блоков
	blockMgr = block.NewBlockManager(chunkAdapter)
	block.RegisterDynamicBlocks(blockMgr)
	block.RegisterInteractiveBlocks(blockMgr)

	// Загружаем чанк из хранилища
	loadedChunk, err := worldStorage.LoadChunk(context.Background(), chunkPos)
	require.NoError(t, err, "Загрузка чанка из хранилища не должно вызывать ошибку")

	// Загружаем блоки из загруженного чанка
	err = blockMgr.LoadBlocksFromChunk(loadedChunk)
	require.NoError(t, err, "Загрузка блоков из чанка не должно вызывать ошибку")

	// Проверяем, что блоки загружены правильно
	// Статический блок
	stoneBlockID := block.GetBlockID(1, 1, chunkPos)
	loadedStoneBlock, err := blockMgr.GetBlock(stoneBlockID)
	assert.NoError(t, err, "Получение статического блока не должно вызывать ошибку")
	assert.Equal(t, chunkmanager.BlockTypeStone, loadedStoneBlock.GetType(),
		"Тип статического блока должен быть BlockTypeStone")

	// Блок огня
	fireBlockID := block.GetBlockID(2, 2, chunkPos)
	loadedFireBlock, err := blockMgr.GetBlock(fireBlockID)
	assert.NoError(t, err, "Получение блока огня не должно вызывать ошибку")
	assert.Equal(t, int32(block.BlockTypeFire), loadedFireBlock.GetType(), "Тип блока должен быть BlockTypeFire")

	// Проверяем свойства блока огня
	intensityStr, exists := loadedFireBlock.(*block.FireBlock).BaseBlock.GetProperty(block.FirePropertyIntensity)
	assert.True(t, exists, "Свойство интенсивности должно существовать")
	assert.Equal(t, "5", intensityStr, "Интенсивность огня должна быть 5")

	// Блок двери
	doorBlockID := block.GetBlockID(3, 3, chunkPos)
	loadedDoorBlock, err := blockMgr.GetBlock(doorBlockID)
	assert.NoError(t, err, "Получение блока двери не должно вызывать ошибку")
	assert.Equal(t, int32(block.BlockTypeDoor), loadedDoorBlock.GetType(), "Тип блока должен быть BlockTypeDoor")

	// Проверяем свойства блока двери
	stateStr, exists := loadedDoorBlock.(*block.DoorBlock).BaseBlock.GetProperty(block.DoorPropertyState)
	assert.True(t, exists, "Свойство состояния должно существовать")
	assert.Equal(t, "open", stateStr, "Состояние двери должно быть 'open'")

	// Проверяем, что чанк помечен как активный из-за наличия динамических блоков
	isActive := chunkMgr.IsChunkActive(chunkPos)
	assert.True(t, isActive, "Чанк с динамическими блоками должен быть помечен как активный")
}

// TestDeltaStorageWithBlocks тестирует сериализацию дельт блоков
func TestDeltaStorageWithBlocks(t *testing.T) {
	// Создаем временную директорию для хранилища
	tempDir, err := os.MkdirTemp("", "block_delta_storage_test")
	require.NoError(t, err, "Создание временной директории не должно вызывать ошибку")
	defer os.RemoveAll(tempDir)

	// Создаем конфигурацию хранилища
	storageConfig := TestStorageConfig{
		WorldPath: filepath.Join(tempDir, "world"),
		WorldName: "test_world",
		WorldSeed: 1234567890,
	}

	// Создаем хранилище
	worldStorage, err := storage.NewBinaryStorage(storageConfig.WorldPath, storageConfig.WorldName, storageConfig.WorldSeed)
	require.NoError(t, err, "Создание хранилища не должно вызывать ошибку")
	defer worldStorage.Close()

	// Создаем ChunkManager и BlockManager
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	chunkMgr := chunkmanager.NewChunkManagerWithStorage(rnd, worldStorage)
	chunkAdapter := &TestChunkManagerAdapter{ChunkManager: chunkMgr}
	blockMgr := block.NewBlockManager(chunkAdapter)

	// Регистрируем фабрики блоков
	block.RegisterDynamicBlocks(blockMgr)
	block.RegisterInteractiveBlocks(blockMgr)

	// Создаем тестовый чанк
	chunkPos := &game.ChunkPosition{X: 3, Y: 4}
	chunk, err := chunkMgr.GetOrGenerateChunk(chunkPos)
	require.NoError(t, err, "Получение чанка не должно вызывать ошибку")

	// Добавляем блок двери
	doorBlock, err := blockMgr.CreateBlock(5, 5, chunkPos, block.BlockTypeDoor)
	require.NoError(t, err, "Создание блока двери не должно вызывать ошибку")

	// Сохраняем блоки в чанк
	err = blockMgr.SaveBlocksToChunk(chunk)
	require.NoError(t, err, "Сохранение блоков в чанк не должно вызывать ошибку")

	// Сохраняем чанк в хранилище
	err = worldStorage.SaveChunk(context.Background(), chunk)
	require.NoError(t, err, "Сохранение чанка в хранилище не должно вызывать ошибку")

	// Изменяем состояние двери
	doorInteractive := doorBlock.(block.InteractiveBlock)
	_, err = doorInteractive.OnInteract("player1", block.InteractionTypeUse, nil)
	require.NoError(t, err, "Взаимодействие с дверью не должно вызывать ошибку")

	// Сохраняем чанк снова, чтобы создать дельту
	err = blockMgr.SaveBlocksToChunk(chunk)
	require.NoError(t, err, "Сохранение блоков в чанк не должно вызывать ошибку")

	err = worldStorage.SaveChunk(context.Background(), chunk)
	require.NoError(t, err, "Сохранение чанка в хранилище не должно вызывать ошибку")

	// Очищаем кеш менеджера блоков
	blockMgr = block.NewBlockManager(chunkAdapter)
	block.RegisterDynamicBlocks(blockMgr)
	block.RegisterInteractiveBlocks(blockMgr)

	// Загружаем чанк из хранилища
	loadedChunk, err := worldStorage.LoadChunk(context.Background(), chunkPos)
	require.NoError(t, err, "Загрузка чанка из хранилища не должно вызывать ошибку")

	// Загружаем блоки из загруженного чанка
	err = blockMgr.LoadBlocksFromChunk(loadedChunk)
	require.NoError(t, err, "Загрузка блоков из чанка не должно вызывать ошибку")

	// Проверяем, что дверь загружена в открытом состоянии
	doorBlockID := block.GetBlockID(5, 5, chunkPos)
	loadedDoorBlock, err := blockMgr.GetBlock(doorBlockID)
	assert.NoError(t, err, "Получение блока двери не должно вызывать ошибку")

	stateStr, exists := loadedDoorBlock.(*block.DoorBlock).BaseBlock.GetProperty(block.DoorPropertyState)
	assert.True(t, exists, "Свойство состояния должно существовать")
	assert.Equal(t, "open", stateStr, "Состояние двери после загрузки должно быть 'open'")
}

// TestWorldServiceBlockIntegration тестирует интеграцию блоков с WorldService
// В реальной системе здесь бы тестировалось взаимодействие с другими частями системы
func TestWorldServiceBlockIntegration(t *testing.T) {
	// Создаем временную директорию для хранилища
	tempDir, err := os.MkdirTemp("", "world_service_block_test")
	require.NoError(t, err, "Создание временной директории не должно вызывать ошибку")
	defer os.RemoveAll(tempDir)

	// Создаем конфигурацию хранилища
	storageConfig := TestStorageConfig{
		WorldPath: filepath.Join(tempDir, "world"),
		WorldName: "test_world",
		WorldSeed: 1234567890,
	}

	// Создаем хранилище
	worldStorage, err := storage.NewBinaryStorage(storageConfig.WorldPath, storageConfig.WorldName, storageConfig.WorldSeed)
	require.NoError(t, err, "Создание хранилища не должно вызывать ошибку")
	defer worldStorage.Close()

	// В реальной системе здесь бы создавался WorldService, но для тестов достаточно отдельно проверить интеграцию
	// с ChunkManager и BlockManager

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	chunkMgr := chunkmanager.NewChunkManagerWithStorage(rnd, worldStorage)
	chunkAdapter := &TestChunkManagerAdapter{ChunkManager: chunkMgr}
	blockMgr := block.NewBlockManager(chunkAdapter)

	// Регистрируем фабрики блоков
	block.RegisterDynamicBlocks(blockMgr)
	block.RegisterInteractiveBlocks(blockMgr)

	// Создаем сундук (интерактивный блок)
	chunkPos := &game.ChunkPosition{X: 5, Y: 6}
	chestBlock, err := blockMgr.CreateBlock(7, 7, chunkPos, block.BlockTypeChest)
	require.NoError(t, err, "Создание блока сундука не должно вызывать ошибку")
	assert.NotNil(t, chestBlock, "Блок сундука должен быть создан")

	// Добавляем предметы в сундук через взаимодействие
	playerID := "player1"
	itemData := map[string]string{
		"item_id":   "42",
		"quantity":  "10",
		"name":      "Алмазный меч",
		"item_data": "{\"enchantments\": [\"sharpness\", \"unbreaking\"]}",
	}

	// Взаимодействуем с сундуком
	event, err := blockMgr.InteractWithBlock(playerID, 7, 7, chunkPos, block.InteractionTypePutItem, itemData)
	require.NoError(t, err, "Взаимодействие с сундуком не должно вызывать ошибку")
	require.NotNil(t, event, "Должно быть создано событие")

	// Сохраняем чанк
	chunk, err := chunkMgr.GetOrGenerateChunk(chunkPos)
	require.NoError(t, err, "Получение чанка не должно вызывать ошибку")

	err = blockMgr.SaveBlocksToChunk(chunk)
	require.NoError(t, err, "Сохранение блоков в чанк не должно вызывать ошибку")

	err = worldStorage.SaveChunk(context.Background(), chunk)
	require.NoError(t, err, "Сохранение чанка в хранилище не должно вызывать ошибку")

	// Очищаем кеш менеджера блоков
	blockMgr = block.NewBlockManager(chunkAdapter)
	block.RegisterDynamicBlocks(blockMgr)
	block.RegisterInteractiveBlocks(blockMgr)

	// Загружаем чанк из хранилища
	loadedChunk, err := worldStorage.LoadChunk(context.Background(), chunkPos)
	require.NoError(t, err, "Загрузка чанка из хранилища не должно вызывать ошибку")

	// Загружаем блоки из загруженного чанка
	err = blockMgr.LoadBlocksFromChunk(loadedChunk)
	require.NoError(t, err, "Загрузка блоков из чанка не должно вызывать ошибку")

	// Проверяем, что сундук загружен с предметами
	chestBlockID := block.GetBlockID(7, 7, chunkPos)
	loadedChestBlock, err := blockMgr.GetBlock(chestBlockID)
	assert.NoError(t, err, "Получение блока сундука не должно вызывать ошибку")

	contentsStr, exists := loadedChestBlock.(*block.ChestBlock).BaseBlock.GetProperty(block.ChestPropertyContents)
	assert.True(t, exists, "Свойство содержимого должно существовать")
	assert.Contains(t, contentsStr, "Алмазный меч", "Содержимое сундука должно включать 'Алмазный меч'")
}
