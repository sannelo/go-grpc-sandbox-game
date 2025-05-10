// block_system.go
package gameloop

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// Константы для настройки системы регионов
const (
	// Размер региона в чанках (например, 4x4 чанка = один регион)
	RegionSize = 4

	// Максимальное количество регионов, обновляемых за один тик
	MaxRegionsPerTick = 1024

	// Базовые интервалы обновления для разных приоритетов (мс)
	HighPriorityInterval   = 200  // Высокий приоритет: 0.2 сек
	MediumPriorityInterval = 500  // Средний приоритет: 0.5 сек
	LowPriorityInterval    = 2000 // Низкий приоритет: 2 сек

	// Весовые коэффициенты для расчета приоритета
	PlayerWeight     = 10 // Вес присутствия игрока в регионе
	SmartBlockWeight = 5  // Вес наличия смарт-блоков
	DirtyWeight      = 0  // Вес состояния "грязности" (изменения)
)

// BlockSystem реализует игровую систему для обновления блоков по регионам
type BlockSystem struct {
	deps         Dependencies
	regions      map[string]*Region
	regionsMu    sync.RWMutex
	updateTicker int64 // счетчик тиков для выбора частоты обновления
}

// Region представляет группу чанков для приоритезации
type Region struct {
	// Координаты региона (в единицах регионов)
	X, Y int

	// Список чанков в регионе
	ChunkPositions []*game.ChunkPosition

	// Статистика региона для определения приоритета
	PlayersCount    int // Количество игроков в регионе
	SmartBlockCount int // Количество smart блоков в регионе
	DirtyBlocks     int // Количество измененных блоков

	// Рассчитанный приоритет обновления (выше = важнее)
	Priority int

	// Последнее время обновления региона
	LastUpdated time.Time

	// Интервал обновления (рассчитывается на основе приоритета)
	UpdateInterval time.Duration
}

// NewBlockSystem создает новую систему обновления блоков
func NewBlockSystem() *BlockSystem {
	return &BlockSystem{
		regions: make(map[string]*Region),
	}
}

func (b *BlockSystem) Name() string { return "block_system" }

func (b *BlockSystem) Init(deps Dependencies) error {
	b.deps = deps
	// Инициализируем регионы на основе активных чанков
	b.refreshRegions()
	return nil
}

// refreshRegions синхронизирует регионы с текущими активными чанками
func (b *BlockSystem) refreshRegions() {
	active := b.deps.Chunks.GetActiveChunks()
	b.regionsMu.Lock()
	defer b.regionsMu.Unlock()
	// Добавляем новые чанки
	for key := range active {
		parts := strings.Split(key, ":")
		if len(parts) != 2 {
			continue
		}
		x, err1 := strconv.Atoi(parts[0])
		y, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			continue
		}
		b.addChunkToRegion(&game.ChunkPosition{X: int32(x), Y: int32(y)})
	}
	// TODO: удалить пропавшие чанки из регионов при необходимости
}

func (b *BlockSystem) Tick(ctx context.Context, dt time.Duration) {
	b.updateTicker++

	// Различные интервалы обновления для разных категорий приоритета
	updateHighPriority := b.updateTicker%4 == 0    // Каждые 4 тика (~200мс при 20TPS)
	updateMediumPriority := b.updateTicker%10 == 0 // Каждые 10 тиков (~500мс при 20TPS)
	updateLowPriority := b.updateTicker%40 == 0    // Каждые 40 тиков (~2сек при 20TPS)

	// Каждые 20 тиков (1 секунда при 20TPS) обновляем приоритеты регионов
	if b.updateTicker%20 == 0 {
		b.refreshRegions()
		b.updateAllRegionsPriorities()
	}

	// Выбираем регионы для обновления
	regionsToUpdate := b.selectRegionsForUpdate(updateHighPriority, updateMediumPriority, updateLowPriority)

	// Обновляем выбранные регионы
	for _, region := range regionsToUpdate {
		b.updateRegion(region)
	}
}

// updateAllRegionsPriorities рассчитывает приоритеты всех регионов
func (b *BlockSystem) updateAllRegionsPriorities() {
	b.regionsMu.Lock()
	defer b.regionsMu.Unlock()

	// Обновляем статистику для каждого региона
	for _, region := range b.regions {
		// Сбрасываем счетчики
		region.PlayersCount = 0
		region.SmartBlockCount = 0
		region.DirtyBlocks = 0

		// Проходим по всем чанкам в регионе
		for _, chunkPos := range region.ChunkPositions {
			// Получаем чанк
			chunk, err := b.deps.Chunks.GetOrGenerateChunk(chunkPos)
			if err != nil {
				continue
			}

			// Считаем smart блоки в чанке
			for _, block := range chunk.Blocks {
				if _, isDynamic := block.Properties["_isDynamic"]; isDynamic {
					region.SmartBlockCount++
				}
				if _, isDirty := block.Properties["_isDirty"]; isDirty {
					region.DirtyBlocks++
				}
			}

			// Проверяем наличие игроков в чанке
			playersInChunk := b.countPlayersInChunk(chunkPos)
			region.PlayersCount += playersInChunk
		}

		// Рассчитываем общий приоритет
		region.Priority = (region.PlayersCount * PlayerWeight) +
			(region.SmartBlockCount * SmartBlockWeight) +
			(region.DirtyBlocks * DirtyWeight)

		// Устанавливаем интервал обновления на основе приоритета
		if region.Priority > 50 {
			region.UpdateInterval = HighPriorityInterval * time.Millisecond
		} else if region.Priority > 20 {
			region.UpdateInterval = MediumPriorityInterval * time.Millisecond
		} else {
			region.UpdateInterval = LowPriorityInterval * time.Millisecond
		}
	}
}

// countPlayersInChunk подсчитывает количество игроков в заданном чанке
func (b *BlockSystem) countPlayersInChunk(chunkPos *game.ChunkPosition) int {
	count := 0

	// Получаем список всех игроков
	players := b.deps.Players.GetAllPlayers()

	// Проверяем каждого игрока
	for _, player := range players {
		// Получаем позицию игрока в мировых координатах
		playerX := player.Position.X
		playerY := player.Position.Y

		// Преобразуем в координаты чанков
		playerChunkX := int32(playerX) / chunkmanager.ChunkSize
		playerChunkY := int32(playerY) / chunkmanager.ChunkSize

		// Если игрок находится в данном чанке, увеличиваем счетчик
		if playerChunkX == chunkPos.X && playerChunkY == chunkPos.Y {
			count++
		}
	}

	return count
}

// selectRegionsForUpdate выбирает регионы для обновления на основе приоритета
func (b *BlockSystem) selectRegionsForUpdate(updateHigh, updateMedium, updateLow bool) []*Region {
	b.regionsMu.RLock()
	defer b.regionsMu.RUnlock()

	now := time.Now()

	// Создаем список кандидатов
	candidates := make([]*Region, 0, len(b.regions))

	// Фильтруем регионы по времени последнего обновления и флагам приоритета
	for _, region := range b.regions {
		// Проверяем, нужно ли обновлять регион в зависимости от его приоритета
		if (region.Priority > 50 && updateHigh) ||
			(region.Priority > 20 && region.Priority <= 50 && updateMedium) ||
			(region.Priority <= 20 && updateLow) {

			// Проверяем, прошло ли достаточно времени с последнего обновления
			if now.Sub(region.LastUpdated) >= region.UpdateInterval {
				candidates = append(candidates, region)
			}
		}
	}

	// Сортируем кандидатов по приоритету (сначала высший)
	sortRegionsByPriority(candidates)

	// Выбираем не более MaxRegionsPerTick регионов
	count := len(candidates)
	if count > MaxRegionsPerTick {
		count = MaxRegionsPerTick
	}

	return candidates[:count]
}

// sortRegionsByPriority сортирует регионы по приоритету (по убыванию)
func sortRegionsByPriority(regions []*Region) {
	// Реализуем простую сортировку пузырьком для наглядности
	n := len(regions)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if regions[j].Priority < regions[j+1].Priority {
				regions[j], regions[j+1] = regions[j+1], regions[j]
			}
		}
	}
}

// updateRegion обновляет блоки в заданном регионе
func (b *BlockSystem) updateRegion(region *Region) {
	region.LastUpdated = time.Now()
	for _, chunkPos := range region.ChunkPositions {
		if !b.deps.Chunks.IsChunkActive(chunkPos) {
			continue
		}
		// Получаем чанк
		chunk, err := b.deps.Chunks.GetOrGenerateChunk(chunkPos)
		if err != nil {
			continue
		}
		// Загружаем Smart Blocks из чанка
		b.deps.Chunks.LoadSmartBlocks(chunk)
		// TODO: здесь позже вызвать обновление динамических блоков в регионе
	}
	log.Printf("Updated region at (%d,%d) with priority %d, contains %d smart blocks, %d players",
		region.X, region.Y, region.Priority, region.SmartBlockCount, region.PlayersCount)
}

// getRegionKey генерирует уникальный ключ для региона
func getRegionKey(regionX, regionY int) string {
	return fmt.Sprintf("r:%d:%d", regionX, regionY)
}

// getRegionForChunk определяет регион, которому принадлежит чанк
func getRegionForChunk(chunkX, chunkY int32) (int, int) {
	// Определяем координаты региона (округляем вниз)
	regionX := int(chunkX) / RegionSize
	regionY := int(chunkY) / RegionSize

	return regionX, regionY
}

// getOrCreateRegion возвращает существующий регион или создает новый
func (b *BlockSystem) getOrCreateRegion(regionX, regionY int) *Region {
	key := getRegionKey(regionX, regionY)

	b.regionsMu.RLock()
	region, exists := b.regions[key]
	b.regionsMu.RUnlock()

	if !exists {
		// Создаем новый регион
		region = &Region{
			X:              regionX,
			Y:              regionY,
			ChunkPositions: make([]*game.ChunkPosition, 0),
			LastUpdated:    time.Now().Add(-24 * time.Hour), // Стартуем с "давно обновленного" состояния
			UpdateInterval: LowPriorityInterval * time.Millisecond,
		}

		b.regionsMu.Lock()
		b.regions[key] = region
		b.regionsMu.Unlock()
	}

	return region
}

// addChunkToRegion добавляет чанк в соответствующий регион
func (b *BlockSystem) addChunkToRegion(chunkPos *game.ChunkPosition) {
	regionX, regionY := getRegionForChunk(chunkPos.X, chunkPos.Y)
	region := b.getOrCreateRegion(regionX, regionY)

	// Проверяем, не добавлен ли уже этот чанк
	for _, existingPos := range region.ChunkPositions {
		if existingPos.X == chunkPos.X && existingPos.Y == chunkPos.Y {
			return // Чанк уже добавлен
		}
	}

	// Добавляем чанк в регион
	b.regionsMu.Lock()
	region.ChunkPositions = append(region.ChunkPositions, chunkPos)
	b.regionsMu.Unlock()
}
