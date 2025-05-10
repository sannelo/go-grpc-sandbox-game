## Система регионов (BigChunks) для оптимизации обновления Smart Blocks

Предлагаю создать следующие компоненты:

### 1. BlockSystemImpl - система для интеграции в gameloop

```go
// block_system.go
package gameloop

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/annelo/go-grpc-server/internal/block"
	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/world"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// Константы для настройки системы регионов
const (
	// Размер региона в чанках (например, 4x4 чанка = один регион)
	RegionSize = 4
	
	// Максимальное количество регионов, обновляемых за один тик
	MaxRegionsPerTick = 10
	
	// Базовые интервалы обновления для разных приоритетов (мс)
	HighPriorityInterval   = 200  // Высокий приоритет: 0.2 сек
	MediumPriorityInterval = 500  // Средний приоритет: 0.5 сек
	LowPriorityInterval    = 2000 // Низкий приоритет: 2 сек
	
	// Весовые коэффициенты для расчета приоритета
	PlayerWeight     = 10  // Вес присутствия игрока в регионе
	SmartBlockWeight = 5   // Вес наличия смарт-блоков
	DirtyWeight      = 3   // Вес состояния "грязности" (изменения)
)

// BlockSystem реализует игровую систему для обновления блоков по регионам
type BlockSystem struct {
	deps          Dependencies
	world         *world.World
	regions       map[string]*Region
	regionsMu     sync.RWMutex
	updateTicker  int64 // счетчик тиков для выбора частоты обновления
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
func NewBlockSystem(gameWorld *world.World) *BlockSystem {
	return &BlockSystem{
		world:   gameWorld,
		regions: make(map[string]*Region),
	}
}

func (b *BlockSystem) Name() string { return "block_system" }

func (b *BlockSystem) Init(deps Dependencies) error {
	b.deps = deps
	
	// Инициализируем регионы на основе существующих чанков
	b.initRegions()
	
	return nil
}

// initRegions создает начальную карту регионов
func (b *BlockSystem) initRegions() {
	// Получаем все активные чанки
	activeChunks := b.deps.Chunks.GetActiveChunks()
	
	for chunkKey := range activeChunks {
		// Пропустим парсинг ключа чанка для простоты примера
		// В реальном коде нужно получить координаты чанка из ключа
		
		// Для каждого чанка определяем регион и добавляем чанк в этот регион
		// ...
	}
	
	// Выполняем начальный расчет приоритета для всех регионов
	b.updateAllRegionsPriorities()
}

func (b *BlockSystem) Tick(ctx context.Context, dt time.Duration) {
	// Увеличиваем счетчик тиков
	b.updateTicker++
	
	// Различные интервалы обновления для разных категорий приоритета
	updateHighPriority := b.updateTicker % 4 == 0    // Каждые 4 тика (~200мс при 20TPS)
	updateMediumPriority := b.updateTicker % 10 == 0 // Каждые 10 тиков (~500мс при 20TPS)
	updateLowPriority := b.updateTicker % 40 == 0    // Каждые 40 тиков (~2сек при 20TPS)
	
	// Каждые 20 тиков (1 секунда при 20TPS) обновляем приоритеты регионов
	if b.updateTicker % 20 == 0 {
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
			chunk, err := b.deps.Chunks.GetChunk(chunkPos)
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
	
	result := make([]*Region, 0, MaxRegionsPerTick)
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
	// Отмечаем время последнего обновления
	region.LastUpdated = time.Now()
	
	// Перебираем все чанки в регионе
	for _, chunkPos := range region.ChunkPositions {
		// Проверяем флаг активности чанка
		if !b.deps.Chunks.IsChunkActive(chunkPos) {
			continue
		}
		
		// Получаем чанк
		chunk, err := b.deps.Chunks.GetChunk(chunkPos)
		if err != nil {
			continue
		}
		
		// Загружаем Smart Blocks из чанка, если они еще не загружены
		b.deps.Chunks.LoadSmartBlocks(chunk)
	}
	
	// Логирование для отладки
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
			X: regionX,
			Y: regionY,
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
```

### 2. Модификация структуры ChunkManager для поддержки "грязных" блоков

```go
// Добавить в chunk_manager.go

// MarkBlockDirty отмечает блок как "грязный" для приоритетного обновления
func (cm *ChunkManager) MarkBlockDirty(blockX, blockY int32, chunkPos *game.ChunkPosition, isDirty bool) {
	// Получаем чанк
	chunk, err := cm.GetChunk(chunkPos)
	if err != nil {
		return
	}
	
	// Ищем блок в чанке
	for _, block := range chunk.Blocks {
		if block.X == blockX && block.Y == blockY {
			if isDirty {
				// Отмечаем блок как "грязный"
				if block.Properties == nil {
					block.Properties = make(map[string]string)
				}
				block.Properties["_isDirty"] = "true"
			} else {
				// Снимаем отметку "грязный"
				if block.Properties != nil {
					delete(block.Properties, "_isDirty")
				}
			}
			break
		}
	}
	
	// Отмечаем чанк как активный, если блок грязный
	if isDirty {
		cm.MarkChunkAsActive(chunkPos)
	}
}

// GetDirtyBlocksCount возвращает количество "грязных" блоков в чанке
func (cm *ChunkManager) GetDirtyBlocksCount(chunkPos *game.ChunkPosition) int {
	count := 0
	
	// Получаем чанк
	chunk, err := cm.GetChunk(chunkPos)
	if err != nil {
		return 0
	}
	
	// Считаем "грязные" блоки
	for _, block := range chunk.Blocks {
		if block.Properties != nil {
			if _, isDirty := block.Properties["_isDirty"]; isDirty {
				count++
			}
		}
	}
	
	return count
}
```

### 3. Модификация BlockManager для работы с регионами

```go
// Добавить в block_manager.go

// UpdateRegionBlocks обновляет все активные блоки в указанных чанках
func (bm *BlockManager) UpdateRegionBlocks(ctx context.Context, chunkPositions []*game.ChunkPosition) {
	now := time.Now()
	
	// Создаем карту блоков для обновления
	blocksToUpdate := make(map[string]bool)
	
	// Собираем все блоки из указанных чанков
	bm.dynamicMu.RLock()
	for blockID, block := range bm.dynamicBlocks {
		_, _, chunkPos := block.GetPosition()
		
		// Проверяем, входит ли чанк блока в список обновляемых
		for _, updateChunkPos := range chunkPositions {
			if chunkPos.X == updateChunkPos.X && chunkPos.Y == updateChunkPos.Y {
				// Проверяем, наступило ли время обновления блока
				scheduleInfo := block.GetScheduleInfo()
				if now.After(scheduleInfo.NextTick) {
					blocksToUpdate[blockID] = true
				}
				break
			}
		}
	}
	bm.dynamicMu.RUnlock()
	
	// Обновляем найденные блоки
	for blockID := range blocksToUpdate {
		bm.updateSingleBlock(blockID, now)
	}
}

// updateSingleBlock обновляет один блок
func (bm *BlockManager) updateSingleBlock(blockID string, now time.Time) {
	// Получаем блок
	bm.dynamicMu.RLock()
	block, exists := bm.dynamicBlocks[blockID]
	bm.dynamicMu.RUnlock()
	
	if !exists {
		return
	}
	
	// Получаем соседей
	neighbors, err := bm.GetNeighborBlocks(blockID)
	if err != nil {
		// Планируем повторную попытку
		scheduleInfo := block.GetScheduleInfo()
		bm.scheduleBlockUpdate(blockID, now.Add(10*time.Second), scheduleInfo.Priority)
		return
	}
	
	// Обновляем блок
	changed := block.Tick(bm.tickingInterval, neighbors)
	
	// Если блок изменился
	if changed {
		// Обновляем состояние блока
		bm.UpdateBlock(block)
		
		// Отмечаем блок как "грязный"
		x, y, chunkPos := block.GetPosition()
		if cm, ok := bm.chunkManager.(interface {
			MarkBlockDirty(blockX, blockY int32, chunkPos *game.ChunkPosition, isDirty bool)
		}); ok {
			cm.MarkBlockDirty(x, y, chunkPos, true)
		}
	}
	
	// Планируем следующее обновление
	scheduleInfo := block.GetScheduleInfo()
	bm.scheduleBlockUpdate(blockID, scheduleInfo.NextTick, scheduleInfo.Priority)
}
```

### 4. Интеграция системы в игровой цикл

```go
// В server.go или main.go

func setupGameLoop(ctx context.Context, chunkManager *chunkmanager.ChunkManager, 
                  blockManager *block.BlockManager, playerManager *playermanager.PlayerManager) {
	
	// Создаем экземпляр мира
	gameWorld := world.NewWorld(rand.New(rand.NewSource(time.Now().UnixNano())), storage)
	
	// Создаем обработчик событий
	emitEvent := func(event *game.WorldEvent) {
		// Реализация широковещательной рассылки событий
		// ...
	}
	
	// Зависимости для систем
	deps := gameloop.Dependencies{
		Players:        playerManager,
		Chunks:         chunkManager,
		EmitWorldEvent: emitEvent,
	}
	
	// Создаем системы
	timeSystem := gameloop.NewTimeSystem()
	weatherSystem := gameloop.NewWeatherSystem(time.Now().UnixNano())
	blockSystem := gameloop.NewBlockSystem(gameWorld)
	
	// Создаем и запускаем игровой цикл
	loop := gameloop.NewLoop(50*time.Millisecond, deps, timeSystem, weatherSystem, blockSystem)
	go loop.Run(ctx)
}
```

### 5. Расширенная статистика активности регионов

```go
// Добавить в block_system.go

// RegionStats содержит информацию о регионе для отладки и мониторинга
type RegionStats struct {
	RegionX, RegionY int
	ChunksCount      int
	PlayersCount     int
	SmartBlockCount  int
	DirtyBlockCount  int
	Priority         int
	LastUpdated      time.Time
	UpdateInterval   time.Duration
}

// GetRegionsStats возвращает статистику по всем регионам
func (b *BlockSystem) GetRegionsStats() []RegionStats {
	b.regionsMu.RLock()
	defer b.regionsMu.RUnlock()
	
	stats := make([]RegionStats, 0, len(b.regions))
	
	for _, region := range b.regions {
		stats = append(stats, RegionStats{
			RegionX:         region.X,
			RegionY:         region.Y,
			ChunksCount:     len(region.ChunkPositions),
			PlayersCount:    region.PlayersCount,
			SmartBlockCount: region.SmartBlockCount,
			DirtyBlockCount: region.DirtyBlocks,
			Priority:        region.Priority,
			LastUpdated:     region.LastUpdated,
			UpdateInterval:  region.UpdateInterval,
		})
	}
	
	return stats
}
```

## Преимущества предложенной системы:

1. **Эффективное распределение вычислительных ресурсов**:
   - Наиболее важные регионы (с игроками и активными блоками) обновляются чаще
   - Малоактивные регионы получают меньше ресурсов CPU

2. **Гибкая приоритезация**:
   - Многофакторная система оценки важности региона
   - Автоматическая адаптация интервалов обновления

3. **Масштабируемость**:
   - Система справится с большими мирами, фокусируясь только на активных регионах
   - Легко настраивается под разные условия нагрузки

4. **Интеграция с существующим кодом**:
   - Использует имеющиеся системы ChunkManager и BlockManager
   - Не требует переписывания существующей логики

5. **Мониторинг и отладка**:
   - Сбор статистики для анализа
   - Возможность логирования активности

## Потенциальные проблемы и их решения:

1. **Синхронизация регионов на границах**:
   - Проблема: Блоки на границах регионов могут быть зависимы от блоков в соседних регионах
   - Решение: Проверять "соседство" регионов и обновлять их в соответствующей последовательности

2. **Балансировка нагрузки**:
   - Проблема: Многие игроки в одном регионе создадут чрезмерную нагрузку
   - Решение: Добавить верхний лимит времени на обработку региона

3. **Отслеживание перемещения игроков**:
   - Проблема: Нужно оперативно отслеживать переход игроков между регионами
   - Решение: Добавить событие перемещения и обновлять приоритеты при изменении позиции

4. **Память и производительность**:
   - Проблема: Хранение информации о регионах требует дополнительной памяти
   - Решение: Очищать неактивные регионы и использовать эффективные структуры данных

5. **Отладка и мониторинг**:
   - Проблема: Сложно отслеживать работу системы приоритетов
   - Решение: Добавить подробное логирование и инструменты визуализации

## Заключение

Предложенная система регионов (BigChunks) позволит эффективно управлять обновлением Smart Blocks в зависимости от их важности. Система адаптируется к игровой активности, сосредотачивая вычислительные ресурсы там, где они наиболее необходимы - рядом с игроками и в регионах с активными процессами.

Интеграция с существующим игровым циклом осуществляется через создание новой системы, которая работает в соответствии с интерфейсом System, что обеспечивает плавное внедрение без нарушения работы остальных компонентов.
