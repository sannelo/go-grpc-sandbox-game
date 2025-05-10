package chunkmanager

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/annelo/go-grpc-server/internal/noisegeneration"
	"github.com/annelo/go-grpc-server/internal/storage"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// Константы для генерации чанков
const (
	// Размер чанка (количество блоков по одной стороне)
	ChunkSize = 16

	// Типы блоков
	BlockTypeAir       = 0
	BlockTypeGrass     = 1
	BlockTypeDirt      = 2
	BlockTypeStone     = 3
	BlockTypeWater     = 4
	BlockTypeSand      = 5
	BlockTypeWood      = 6
	BlockTypeLeaves    = 7
	BlockTypeSnow      = 8
	BlockTypeTallGrass = 9
	BlockTypeFlower    = 10
)

// ChunkManager управляет чанками игрового мира
type ChunkManager struct {
	chunks map[string]*game.Chunk
	mu     sync.RWMutex
	rnd    *rand.Rand

	// Генератор шума для биомов
	biomeNoise *noisegeneration.BiomeNoise

	// Кеш сгенерированных блоков
	blockCache     map[string]byte // Кеш типов блоков (используем byte вместо int32)
	blockCacheMu   sync.RWMutex    // Мьютекс для кеша блоков
	blockCacheHits int             // Счетчик попаданий в кеш
	blockCacheMiss int             // Счетчик промахов кеша

	// Хранилище данных мира
	storage storage.WorldStorage
}

// NewChunkManager создает новый экземпляр менеджера чанков
func NewChunkManager(rnd *rand.Rand) *ChunkManager {
	seed := rnd.Int63()
	return &ChunkManager{
		chunks:     make(map[string]*game.Chunk),
		rnd:        rnd,
		biomeNoise: noisegeneration.NewBiomeNoise(seed),
		blockCache: make(map[string]byte), // Инициализация кеша блоков
	}
}

// NewChunkManagerWithStorage создает новый экземпляр менеджера чанков с хранилищем
func NewChunkManagerWithStorage(rnd *rand.Rand, worldStorage storage.WorldStorage) *ChunkManager {
	cm := NewChunkManager(rnd)
	cm.storage = worldStorage
	return cm
}

// LoadChunksFromStorage загружает чанки из хранилища
func (cm *ChunkManager) LoadChunksFromStorage(ctx context.Context) error {
	if cm.storage == nil {
		return errors.New("хранилище не инициализировано")
	}

	// Получаем список всех чанков в хранилище
	positions, err := cm.storage.ListChunks(ctx)
	if err != nil {
		return fmt.Errorf("ошибка при получении списка чанков: %w", err)
	}

	loaded := 0
	for _, pos := range positions {
		// Загружаем чанк из хранилища
		chunk, err := cm.storage.LoadChunk(ctx, pos)
		if err != nil {
			// Пропускаем чанки, которые не удалось загрузить
			continue
		}

		// Добавляем чанк в память
		cm.mu.Lock()
		key := getChunkKey(pos)
		cm.chunks[key] = chunk
		cm.mu.Unlock()

		loaded++
	}

	return nil
}

// SaveChunksToStorage сохраняет все чанки в хранилище
func (cm *ChunkManager) SaveChunksToStorage(ctx context.Context) error {
	if cm.storage == nil {
		return errors.New("хранилище не инициализировано")
	}

	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Создаем копию карты чанков для итерации
	savedChunks := 0
	for _, chunk := range cm.chunks {
		err := cm.storage.SaveChunk(ctx, chunk)
		if err != nil {
			// Логируем ошибку и продолжаем
			continue
		}
		savedChunks++
	}

	return nil
}

// SaveChunk сохраняет чанк в хранилище
func (cm *ChunkManager) SaveChunk(ctx context.Context, pos *game.ChunkPosition) error {
	if cm.storage == nil {
		return errors.New("хранилище не инициализировано")
	}

	cm.mu.RLock()
	key := getChunkKey(pos)
	chunk, exists := cm.chunks[key]
	cm.mu.RUnlock()

	if !exists {
		return errors.New("чанк не найден")
	}

	return cm.storage.SaveChunk(ctx, chunk)
}

// getChunkKey возвращает строковый ключ для чанка
func getChunkKey(pos *game.ChunkPosition) string {
	return fmt.Sprintf("%d:%d", pos.X, pos.Y)
}

// GetChunk возвращает чанк по его координатам
func (cm *ChunkManager) GetChunk(pos *game.ChunkPosition) (*game.Chunk, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	key := getChunkKey(pos)
	chunk, exists := cm.chunks[key]
	if !exists {
		return nil, errors.New("чанк не найден")
	}

	return chunk, nil
}

// GetOrGenerateChunk возвращает существующий чанк или генерирует новый, если чанк не существует
func (cm *ChunkManager) GetOrGenerateChunk(pos *game.ChunkPosition) (*game.Chunk, error) {
	// Сначала пытаемся получить существующий чанк
	cm.mu.RLock()
	key := getChunkKey(pos)
	chunk, exists := cm.chunks[key]
	cm.mu.RUnlock()

	if exists {
		return chunk, nil
	}

	// Если хранилище инициализировано, пытаемся загрузить чанк из хранилища
	if cm.storage != nil {
		chunk, err := cm.storage.LoadChunk(context.Background(), pos)
		if err == nil {
			// Если чанк успешно загружен, добавляем его в память
			cm.mu.Lock()
			cm.chunks[key] = chunk
			cm.mu.Unlock()
			return chunk, nil
		}

		// Если ошибка не связана с отсутствием чанка, возвращаем её
		var notFoundErr storage.ErrChunkNotFound
		if !errors.As(err, &notFoundErr) {
			return nil, fmt.Errorf("ошибка при загрузке чанка: %w", err)
		}

		// Если чанк не найден в хранилище, генерируем новый
	}

	// Чанк не существует, генерируем новый
	return cm.generateChunk(pos)
}

// generateChunk генерирует новый чанк
func (cm *ChunkManager) generateChunk(pos *game.ChunkPosition) (*game.Chunk, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Проверяем, не был ли чанк создан другой горутиной пока мы ждали
	key := getChunkKey(pos)
	if chunk, exists := cm.chunks[key]; exists {
		return chunk, nil
	}

	// Создаем новый чанк
	chunk := &game.Chunk{
		Position: pos,
		Blocks:   make([]*game.Block, 0, ChunkSize*ChunkSize),
		Entities: make([]*game.Entity, 0),
	}

	// Генерируем блоки для чанка с использованием шума Перлина
	generateBlocksForChunk(chunk, cm)

	// Сохраняем чанк в памяти
	cm.chunks[key] = chunk

	// Если хранилище инициализировано, сохраняем чанк в хранилище
	if cm.storage != nil {
		go func() {
			err := cm.storage.SaveChunk(context.Background(), chunk)
			if err != nil {
				// Логируем ошибку (в реальной системе)
			}
		}()
	}

	return chunk, nil
}

// StartPeriodicalSaving запускает периодическое сохранение чанков
func (cm *ChunkManager) StartPeriodicalSaving(ctx context.Context, interval time.Duration) {
	if cm.storage == nil {
		return // Если хранилище не инициализировано, не запускаем сохранение
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Сохраняем все чанки в хранилище
				err := cm.SaveChunksToStorage(ctx)
				if err != nil {
					// Логируем ошибку (в реальной системе)
				}
			case <-ctx.Done():
				// Сохраняем все чанки перед завершением
				cm.SaveChunksToStorage(context.Background())
				return
			}
		}
	}()
}

// SetBlock устанавливает блок в указанную позицию в чанке
func (cm *ChunkManager) SetBlock(chunkPos *game.ChunkPosition, block *game.Block) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	key := getChunkKey(chunkPos)
	chunk, exists := cm.chunks[key]
	if !exists {
		return errors.New("чанк не найден")
	}

	// Находим блок с такими координатами
	blockFound := false
	for i, existingBlock := range chunk.Blocks {
		if existingBlock.X == block.X && existingBlock.Y == block.Y {
			// Заменяем существующий блок
			chunk.Blocks[i] = block
			blockFound = true
			break
		}
	}

	// Если блок не найден, добавляем новый
	if !blockFound {
		chunk.Blocks = append(chunk.Blocks, block)
	}

	return nil
}

// getBlockCacheKey формирует ключ для кеша блоков
func getBlockCacheKey(x, y float64) string {
	// Округляем координаты для уменьшения вариаций ключей
	// и используем более компактное представление
	intX := int(math.Floor(x))
	intY := int(math.Floor(y))

	// Используем сокращенный формат для ключа
	return fmt.Sprintf("%d:%d", intX, intY)
}

// getCachedBlockType получает тип блока из кеша
func (cm *ChunkManager) getCachedBlockType(x, y float64) (int32, bool) {
	key := getBlockCacheKey(x, y)

	cm.blockCacheMu.RLock()
	blockTypeByte, exists := cm.blockCache[key]
	cm.blockCacheMu.RUnlock()

	if exists {
		cm.blockCacheMu.Lock()
		cm.blockCacheHits++
		cm.blockCacheMu.Unlock()

		// Преобразуем byte в int32
		return int32(blockTypeByte), true
	}

	return 0, false
}

// cacheBlockType сохраняет тип блока в кеше
func (cm *ChunkManager) cacheBlockType(x, y float64, blockType int32) {
	// Проверяем, что тип блока может быть сохранен в byte (0-255)
	if blockType < 0 || blockType > 255 {
		return // Пропускаем блоки за пределами диапазона byte
	}

	key := getBlockCacheKey(x, y)

	cm.blockCacheMu.Lock()
	defer cm.blockCacheMu.Unlock()

	// Ограничиваем размер кеша
	if len(cm.blockCache) > 100000 { // Максимальный размер кеша
		// Простая стратегия очистки - удаляем весь кеш при достижении лимита
		cm.blockCache = make(map[string]byte)
	}

	cm.blockCache[key] = byte(blockType)
}

// GetBlockTypeFromBiomeData определяет тип блока из данных биома с кешированием
func (cm *ChunkManager) GetBlockTypeFromBiomeData(worldX, worldY float64) int32 {
	// Проверяем кеш
	if blockType, exists := cm.getCachedBlockType(worldX, worldY); exists {
		return blockType
	}

	cm.blockCacheMu.Lock()
	cm.blockCacheMiss++
	cm.blockCacheMu.Unlock()

	// Получаем данные о биоме
	height, moisture, temperature := cm.biomeNoise.GetBiomeData(worldX, worldY)

	// Определяем тип блока
	blockType := getBlockTypeFromBiome(height, moisture, temperature)

	// Добавляем случайную вариацию для некоторых типов блоков
	if blockType == BlockTypeGrass && cm.rnd.Float64() < 0.1 {
		// 10% шанс для высокой травы или цветов
		if cm.rnd.Float64() < 0.7 {
			blockType = BlockTypeTallGrass
		} else {
			blockType = BlockTypeFlower
		}
	}

	// Кешируем результат
	cm.cacheBlockType(worldX, worldY, blockType)

	return blockType
}

// GetCacheStats возвращает статистику использования кеша
func (cm *ChunkManager) GetCacheStats() map[string]interface{} {
	cm.blockCacheMu.RLock()
	total := cm.blockCacheHits + cm.blockCacheMiss
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(cm.blockCacheHits) / float64(total)
	}

	stats := map[string]interface{}{
		"block_cache": map[string]interface{}{
			"size":     len(cm.blockCache),
			"hits":     cm.blockCacheHits,
			"misses":   cm.blockCacheMiss,
			"hit_rate": hitRate,
		},
	}
	cm.blockCacheMu.RUnlock()

	// Добавляем статистику кеша шума
	noiseStats := cm.biomeNoise.GetCacheStats()
	for k, v := range noiseStats {
		stats[k] = v
	}

	return stats
}

// getBlockTypeFromBiome определяет тип блока на основе данных биома
func getBlockTypeFromBiome(height, moisture, temperature float64) int32 {
	// Получаем тип биома
	biomeType := noisegeneration.GetBiomeType(height, moisture, temperature)

	// Определяем тип блока в зависимости от биома
	switch biomeType {
	case noisegeneration.BiomeOcean:
		return BlockTypeWater
	case noisegeneration.BiomeBeach:
		return BlockTypeSand
	case noisegeneration.BiomeDesert:
		return BlockTypeSand
	case noisegeneration.BiomelPlains:
		return BlockTypeGrass
	case noisegeneration.BiomeForest:
		return BlockTypeGrass
	case noisegeneration.BiomeTaiga:
		if height > 0.7 {
			return BlockTypeSnow
		}
		return BlockTypeGrass
	case noisegeneration.BiomeMountain:
		if height > 0.85 {
			return BlockTypeSnow
		}
		return BlockTypeStone
	case noisegeneration.BiomeSnowland:
		return BlockTypeSnow
	default:
		return BlockTypeGrass
	}
}

// generateBlocksForChunk генерирует блоки для чанка используя процедурную генерацию с шумом Перлина
func generateBlocksForChunk(chunk *game.Chunk, cm *ChunkManager) {
	chunkX := chunk.Position.X
	chunkY := chunk.Position.Y

	// Генерируем блоки для каждой позиции в чанке
	for y := 0; y < ChunkSize; y++ {
		for x := 0; x < ChunkSize; x++ {
			// Преобразуем локальные координаты в мировые
			worldX := float64(chunkX*ChunkSize + int32(x))
			worldY := float64(chunkY*ChunkSize + int32(y))

			// Получаем тип блока с кешированием
			blockType := cm.GetBlockTypeFromBiomeData(worldX, worldY)

			// Создаем блок
			block := &game.Block{
				X:    int32(x),
				Y:    int32(y),
				Type: blockType,
			}

			// Добавляем блок в чанк
			chunk.Blocks = append(chunk.Blocks, block)
		}
	}
}

// LoadChunk загружает чанк из долговременного хранилища
// Пока просто заглушка для будущей реализации
func (cm *ChunkManager) LoadChunk(pos *game.ChunkPosition) (*game.Chunk, error) {
	// Заглушка для будущей реализации
	return nil, errors.New("чанк не найден в хранилище")
}

// ClearBlockCache очищает кеш блоков
func (cm *ChunkManager) ClearBlockCache() {
	cm.blockCacheMu.Lock()
	defer cm.blockCacheMu.Unlock()

	cm.blockCache = make(map[string]byte)
	// Сбрасываем счетчики
	cm.blockCacheHits = 0
	cm.blockCacheMiss = 0
}

// ClearAllCaches очищает все кеши в менеджере чанков
func (cm *ChunkManager) ClearAllCaches() {
	// Очищаем кеш блоков
	cm.ClearBlockCache()

	// Очищаем кеши шума
	cm.biomeNoise.ClearAllCaches()
}

// StartCacheCleanupRoutine запускает регулярную очистку кеша и выгрузку неиспользуемых чанков
func (cm *ChunkManager) StartCacheCleanupRoutine(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Очищаем неиспользуемые кеши
				cm.cleanupUnusedChunks()
				cm.ClearBlockCache()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// cleanupUnusedChunks выгружает из памяти давно не используемые чанки, предварительно сохраняя их
func (cm *ChunkManager) cleanupUnusedChunks() {
	if cm.storage == nil {
		return // Нет смысла выгружать, если нет хранилища
	}

	chunksToUnload := make([]*game.ChunkPosition, 0)

	// Находим чанки для выгрузки
	cm.mu.RLock()
	for _, chunk := range cm.chunks {
		// Если есть метаданные о последнем доступе, проверяем их
		// В реальной реализации нужно добавить отслеживание времени последнего доступа к чанку

		// Для примера: добавляем каждый 5-й чанк в список на выгрузку
		// В реальном коде условие будет по времени доступа
		if cm.rnd.Intn(5) == 0 {
			chunksToUnload = append(chunksToUnload, chunk.Position)
		}
	}
	cm.mu.RUnlock()

	// Сохраняем и выгружаем чанки
	for _, pos := range chunksToUnload {
		// Сохраняем чанк перед выгрузкой
		err := cm.SaveChunk(context.Background(), pos)
		if err != nil {
			// Логируем ошибку, но продолжаем
			continue
		}

		// Выгружаем чанк из памяти
		cm.mu.Lock()
		key := getChunkKey(pos)
		delete(cm.chunks, key)
		cm.mu.Unlock()
	}
}

// HasStorage возвращает true, если хранилище инициализировано
func (cm *ChunkManager) HasStorage() bool {
	return cm.storage != nil
}
