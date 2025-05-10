package chunkmanager

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/annelo/go-grpc-server/internal/noisegeneration"
	"github.com/annelo/go-grpc-server/internal/storage"
	"github.com/annelo/go-grpc-server/internal/worldinterfaces"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// Константы для генерации чанков
const (
	// Размер чанка (количество блоков по одной стороне)
	ChunkSize int32 = 16

	// Типы блоков
	BlockTypeAir       int32 = 0
	BlockTypeGrass     int32 = 1
	BlockTypeDirt      int32 = 2
	BlockTypeStone     int32 = 3
	BlockTypeWater     int32 = 4
	BlockTypeSand      int32 = 5
	BlockTypeWood      int32 = 6
	BlockTypeLeaves    int32 = 7
	BlockTypeSnow      int32 = 8
	BlockTypeTallGrass int32 = 9
	BlockTypeFlower    int32 = 10
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

	// ActiveChunksSet представляет собой множество чанков, которые должны оставаться активными
	activeChunks map[string]bool

	// Менеджер умных блоков
	blockManager worldinterfaces.BlockManagerInterface
}

// NewChunkManager создает новый экземпляр менеджера чанков
func NewChunkManager(rnd *rand.Rand) *ChunkManager {
	seed := rnd.Int63()
	cm := &ChunkManager{
		chunks:       make(map[string]*game.Chunk),
		rnd:          rnd,
		biomeNoise:   noisegeneration.NewBiomeNoise(seed),
		blockCache:   make(map[string]byte), // Инициализация кеша блоков
		activeChunks: make(map[string]bool), // Инициализация множества активных чанков
	}

	// Блок-менеджер будет инициализирован позже через SetBlockManager
	return cm
}

// NewChunkManagerWithStorage создает новый экземпляр менеджера чанков с хранилищем
func NewChunkManagerWithStorage(rnd *rand.Rand, worldStorage storage.WorldStorage) *ChunkManager {
	cm := NewChunkManager(rnd)
	cm.storage = worldStorage
	return cm
}

// SetBlockManager устанавливает менеджер блоков
func (cm *ChunkManager) SetBlockManager(blockManager worldinterfaces.BlockManagerInterface) {
	cm.blockManager = blockManager
}

// GetBlockManager возвращает менеджер умных блоков
func (cm *ChunkManager) GetBlockManager() worldinterfaces.BlockManagerInterface {
	return cm.blockManager
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
		key := cm.getChunkKey(pos)
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
	key := cm.getChunkKey(pos)
	chunk, exists := cm.chunks[key]
	cm.mu.RUnlock()

	if !exists {
		return errors.New("чанк не найден")
	}

	return cm.storage.SaveChunk(ctx, chunk)
}

// getChunkKey возвращает строковый ключ для чанка
func (cm *ChunkManager) getChunkKey(pos *game.ChunkPosition) string {
	return fmt.Sprintf("%d:%d", pos.X, pos.Y)
}

func (cm *ChunkManager) getPosFromChunkKey(key string) (*game.ChunkPosition, error) {
	parts := strings.Split(key, ":")
	if len(parts) != 2 {
		return nil, errors.New("неверный формат ключа чанка")
	}
	x, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, errors.New("неверный формат ключа чанка")
	}
	y, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, errors.New("неверный формат ключа чанка")
	}

	return &game.ChunkPosition{X: int32(x), Y: int32(y)}, nil
}

// GetChunk возвращает чанк по его координатам
func (cm *ChunkManager) GetChunk(pos *game.ChunkPosition) (*game.Chunk, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	key := cm.getChunkKey(pos)
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
	key := cm.getChunkKey(pos)
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

			// Загружаем умные блоки, если есть менеджер блоков
			if cm.blockManager != nil {
				cm.blockManager.LoadBlocksFromChunk(chunk)
			}

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
	key := cm.getChunkKey(pos)
	if chunk, exists := cm.chunks[key]; exists {
		return chunk, nil
	}

	// Создаем новый чанк
	chunk := &game.Chunk{
		Position: pos,
		Blocks:   make([]*game.Block, 0, ChunkSize*ChunkSize),
		Entities: make([]*game.Entity, 0),
	}

	// Генерируем блоки для чанка
	generateBlocksForChunk(chunk, cm)

	// Сохраняем чанк в памяти
	cm.chunks[key] = chunk

	// Если хранилище инициализировано, сохраняем чанк в хранилище
	if cm.storage != nil {
		// Перед сохранением, если используем умные блоки, сохраняем их состояние в чанк
		if cm.blockManager != nil {
			if err := cm.blockManager.SaveBlocksToChunk(chunk); err != nil {
				// Логируем ошибку (в реальной системе)
			}
		}

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

	key := cm.getChunkKey(chunkPos)
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

	// Если блок динамический, отмечаем чанк как активный без рекурсивного Lock
	if _, isDynamic := block.Properties["_isDynamic"]; isDynamic {
		// inline marking to avoid deadlock (SetBlock already holds mu)
		if cm.activeChunks == nil {
			cm.activeChunks = make(map[string]bool)
		}
		cm.activeChunks[key] = true
	}

	// Если используется Smart Blocks, обновляем блок в менеджере блоков
	if cm.blockManager != nil {
		_, err := cm.blockManager.CreateBlock(block.X, block.Y, chunkPos, block.Type)
		if err != nil {
			// Логируем ошибку, но не прерываем выполнение
			// так как блок уже добавлен в чанк
		}
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

// generateBlocksForChunk генерирует блоки для чанка с использованием Smart Blocks
func generateBlocksForChunk(chunk *game.Chunk, cm *ChunkManager) {
	// Если менеджер блоков еще не инициализирован, используем старый метод
	if cm.blockManager == nil {
		generateBlocksForChunkLegacy(chunk, cm)
		return
	}

	// Используем Smart Blocks для генерации блоков
	for x := int32(0); x < ChunkSize; x++ {
		for y := int32(0); y < ChunkSize; y++ {
			// Преобразуем локальные координаты в мировые для определения биома
			worldX := float64(chunk.Position.X*ChunkSize + x)
			worldY := float64(chunk.Position.Y*ChunkSize + y)

			// Получаем тип блока на основе биома
			blockType := cm.GetBlockTypeFromBiomeData(worldX, worldY)

			// Создаем умный блок через менеджер блоков
			smartBlock, err := cm.blockManager.CreateBlock(x, y, chunk.Position, blockType)
			if err != nil {
				// В случае ошибки используем обычный блок
				block := &game.Block{
					X:    x,
					Y:    y,
					Type: blockType,
				}
				chunk.Blocks = append(chunk.Blocks, block)
				continue
			}

			// Конвертируем в протобуф и добавляем в чанк
			// Здесь используем type assertion, так как интерфейс возвращает interface{}
			if b, ok := smartBlock.(interface{ ToProto() *game.Block }); ok {
				chunk.Blocks = append(chunk.Blocks, b.ToProto())
			} else {
				// Если не удалось конвертировать, используем обычный блок
				block := &game.Block{
					X:    x,
					Y:    y,
					Type: blockType,
				}
				chunk.Blocks = append(chunk.Blocks, block)
			}
		}
	}

	// Отмечаем чанк как активный, если в нем есть динамические блоки
	for _, block := range chunk.Blocks {
		if _, isDynamic := block.Properties["_isDynamic"]; isDynamic {
			cm.MarkChunkAsActive(chunk.Position)
			break
		}
	}
}

// generateBlocksForChunkLegacy - старая версия функции генерации блоков
func generateBlocksForChunkLegacy(chunk *game.Chunk, cm *ChunkManager) {
	for x := int32(0); x < ChunkSize; x++ {
		for y := int32(0); y < ChunkSize; y++ {
			// Преобразуем локальные координаты в мировые для определения биома
			worldX := float64(chunk.Position.X*ChunkSize + x)
			worldY := float64(chunk.Position.Y*ChunkSize + y)

			// Получаем тип блока на основе биома
			blockType := cm.GetBlockTypeFromBiomeData(worldX, worldY)

			block := &game.Block{
				X:    x,
				Y:    y,
				Type: blockType,
			}

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

// cleanupUnusedChunks выгружает из памяти неиспользуемые чанки
func (cm *ChunkManager) cleanupUnusedChunks() {
	// Определяем критерии для удаления чанков из кеша
	// ...

	// Подготавливаем список чанков для удаления
	chunksToRemove := make([]string, 0)
	cm.mu.RLock()
	for key := range cm.chunks {
		// Не выгружаем активные чанки
		if cm.activeChunks != nil && cm.activeChunks[key] {
			continue
		}

		// Здесь можно добавить другие критерии для выгрузки
		// ...

		chunksToRemove = append(chunksToRemove, key)
	}
	cm.mu.RUnlock()

	// Удаляем чанки
	if len(chunksToRemove) > 0 {
		cm.mu.Lock()
		for _, key := range chunksToRemove {
			// Проверяем еще раз, что чанк не стал активным
			if cm.activeChunks != nil && cm.activeChunks[key] {
				continue
			}

			// Удаляем чанк из памяти
			delete(cm.chunks, key)
		}
		cm.mu.Unlock()
	}
}

// HasStorage возвращает true, если хранилище инициализировано
func (cm *ChunkManager) HasStorage() bool {
	return cm.storage != nil
}

// ActiveChunksSet представляет собой множество чанков, которые должны оставаться активными
type ActiveChunksSet map[string]bool

// GetActiveChunks возвращает множество чанков, которые должны оставаться активными
func (cm *ChunkManager) GetActiveChunks() ActiveChunksSet {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	activeChunks := make(ActiveChunksSet)
	for key := range cm.activeChunks {
		activeChunks[key] = true
	}

	return activeChunks
}

// MarkChunkAsActive помечает чанк как активный
func (cm *ChunkManager) MarkChunkAsActive(pos *game.ChunkPosition) {
	key := cm.getChunkKey(pos)

	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.activeChunks == nil {
		cm.activeChunks = make(map[string]bool)
	}

	cm.activeChunks[key] = true
}

// UnmarkChunkAsActive снимает пометку активности с чанка
func (cm *ChunkManager) UnmarkChunkAsActive(pos *game.ChunkPosition) {
	key := cm.getChunkKey(pos)

	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.activeChunks == nil {
		return
	}

	delete(cm.activeChunks, key)
}

// IsChunkActive проверяет, помечен ли чанк как активный
func (cm *ChunkManager) IsChunkActive(pos *game.ChunkPosition) bool {
	key := cm.getChunkKey(pos)

	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if cm.activeChunks == nil {
		return false
	}

	return cm.activeChunks[key]
}

// LoadSmartBlocks загружает умные блоки из чанка в BlockManager
func (cm *ChunkManager) LoadSmartBlocks(chunk *game.Chunk) error {
	if cm.blockManager == nil {
		return nil // Если менеджер блоков не инициализирован, ничего не делаем
	}

	return cm.blockManager.LoadBlocksFromChunk(chunk)
}

// InteractWithBlock позволяет игроку взаимодействовать с блоком
func (cm *ChunkManager) InteractWithBlock(playerID string, x, y int32, chunkPos *game.ChunkPosition, interactionType string, data map[string]string) (*game.WorldEvent, error) {
	// Проверяем, инициализирован ли менеджер блоков
	if cm.blockManager == nil {
		return nil, errors.New("менеджер блоков не инициализирован")
	}

	// Делегируем взаимодействие менеджеру блоков
	return cm.blockManager.InteractWithBlock(playerID, x, y, chunkPos, interactionType, data)
}

// MarkChunkDirty отмечает чанк как измененный для последующего сохранения
func (cm *ChunkManager) MarkChunkDirty(pos *game.ChunkPosition) {
	// Эта функция может быть расширена для оптимизации сохранения
	// Например, можно добавить очередь "грязных" чанков

	// Если чанк содержит динамические блоки, также отмечаем его как активный
	cm.MarkChunkAsActive(pos)
}

// GetChunkSnapshot возвращает безопасную копию чанка для отправки клиенту без конфликтов.
func (cm *ChunkManager) GetChunkSnapshot(pos *game.ChunkPosition) (*game.Chunk, error) {
	// Получаем или генерируем чанк
	origChunk, err := cm.GetOrGenerateChunk(pos)
	if err != nil {
		return nil, fmt.Errorf("не удалось получить чанк [%d,%d]: %w", pos.X, pos.Y, err)
	}
	// Копируем срез Blocks и Entities под мьютексом
	cm.mu.RLock()
	blocksCopy := append([]*game.Block(nil), origChunk.Blocks...)
	entitiesCopy := append([]*game.Entity(nil), origChunk.Entities...)
	cm.mu.RUnlock()

	return &game.Chunk{
		Position: origChunk.Position,
		Blocks:   blocksCopy,
		Entities: entitiesCopy,
	}, nil
}
