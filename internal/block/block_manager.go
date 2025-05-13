package block

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/worldinterfaces"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// BlockFactory - функция-фабрика для создания блоков
type BlockFactory func(x, y int32, chunkPos *game.ChunkPosition, blockType int32) Block

// BlockManager управляет всеми блоками в мире
type BlockManager struct {
	// Блоки, хранящиеся по ключу BlockID
	blocks map[string]Block
	mu     sync.RWMutex

	// Карта динамических блоков
	dynamicBlocks map[string]DynamicBlock
	dynamicMu     sync.RWMutex

	// Карта интерактивных блоков
	interactiveBlocks map[string]InteractiveBlock
	interactiveMu     sync.RWMutex

	// Реестр фабрик блоков по типу
	blockFactories map[int32]BlockFactory
	factoriesMu    sync.RWMutex

	// ChunkManager для взаимодействия с чанками
	chunkManager worldinterfaces.ChunkManagerInterface

	// Календарь обновлений блоков
	updateQueue     []*blockUpdateInfo
	queueMu         sync.Mutex
	tickingInterval time.Duration

	// Канал для событий изменения блоков
	blockEvents chan *game.WorldEvent

	// Время последней отправки изменения для каждого блока
	lastSent map[string]time.Time
}

// blockUpdateInfo содержит информацию о планировании обновления блока
type blockUpdateInfo struct {
	BlockID  string
	NextTick time.Time
	Priority int
}

// NewBlockManager создает новый менеджер блоков
func NewBlockManager(chunkManager worldinterfaces.ChunkManagerInterface) *BlockManager {
	manager := &BlockManager{
		blocks:            make(map[string]Block),
		dynamicBlocks:     make(map[string]DynamicBlock),
		interactiveBlocks: make(map[string]InteractiveBlock),
		blockFactories:    make(map[int32]BlockFactory),
		chunkManager:      chunkManager,
		updateQueue:       make([]*blockUpdateInfo, 0),
		blockEvents:       make(chan *game.WorldEvent, 100),
		lastSent:          make(map[string]time.Time),
		tickingInterval:   100 * time.Millisecond,
	}

	// Регистрируем фабрики для базовых блоков
	manager.RegisterBlockFactory(chunkmanager.BlockTypeAir, NewStaticBlock)
	manager.RegisterBlockFactory(chunkmanager.BlockTypeGrass, NewStaticBlock)
	manager.RegisterBlockFactory(chunkmanager.BlockTypeDirt, NewStaticBlock)
	manager.RegisterBlockFactory(chunkmanager.BlockTypeStone, NewStaticBlock)
	manager.RegisterBlockFactory(chunkmanager.BlockTypeWater, NewStaticBlock)
	manager.RegisterBlockFactory(chunkmanager.BlockTypeSand, NewStaticBlock)
	manager.RegisterBlockFactory(chunkmanager.BlockTypeWood, NewStaticBlock)
	manager.RegisterBlockFactory(chunkmanager.BlockTypeLeaves, NewStaticBlock)
	manager.RegisterBlockFactory(chunkmanager.BlockTypeSnow, NewStaticBlock)
	manager.RegisterBlockFactory(chunkmanager.BlockTypeTallGrass, NewStaticBlock)
	manager.RegisterBlockFactory(chunkmanager.BlockTypeFlower, NewStaticBlock)

	return manager
}

// RegisterBlockFactory регистрирует фабрику для создания блоков определенного типа
func (bm *BlockManager) RegisterBlockFactory(blockType int32, factory BlockFactory) {
	bm.factoriesMu.Lock()
	defer bm.factoriesMu.Unlock()

	bm.blockFactories[blockType] = factory
}

// CreateBlock создает новый блок указанного типа
func (bm *BlockManager) CreateBlock(x, y int32, chunkPos *game.ChunkPosition, blockType int32) (Block, error) {
	bm.factoriesMu.RLock()
	factory, exists := bm.blockFactories[blockType]
	bm.factoriesMu.RUnlock()

	if !exists {
		// Если фабрика не найдена, используем фабрику для статических блоков
		factory = NewStaticBlock
	}

	block := factory(x, y, chunkPos, blockType)
	blockID := block.GetID()

	// Регистрируем блок в соответствующих картах
	bm.mu.Lock()
	bm.blocks[blockID] = block
	bm.mu.Unlock()

	// Для динамических блоков
	if dynamicBlock, ok := block.(DynamicBlock); ok {
		bm.dynamicMu.Lock()
		bm.dynamicBlocks[blockID] = dynamicBlock
		bm.dynamicMu.Unlock()

		// Добавляем в очередь обновлений
		scheduleInfo := dynamicBlock.GetScheduleInfo()
		bm.scheduleBlockUpdate(blockID, scheduleInfo.NextTick, scheduleInfo.Priority)
	}

	// Для интерактивных блоков
	if interactiveBlock, ok := block.(InteractiveBlock); ok {
		bm.interactiveMu.Lock()
		bm.interactiveBlocks[blockID] = interactiveBlock
		bm.interactiveMu.Unlock()
	}

	return block, nil
}

// GetBlock возвращает блок по его ID
func (bm *BlockManager) GetBlock(blockID string) (Block, error) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	block, exists := bm.blocks[blockID]
	if !exists {
		return nil, ErrBlockNotFound
	}

	return block, nil
}

// GetBlockAt возвращает блок по координатам
func (bm *BlockManager) GetBlockAt(x, y int32, chunkPos *game.ChunkPosition) (Block, error) {
	blockID := GetBlockID(x, y, chunkPos)
	return bm.GetBlock(blockID)
}

// GetNeighborBlocks возвращает соседние блоки для указанного блока
func (bm *BlockManager) GetNeighborBlocks(blockID string) (map[string]Block, error) {
	block, err := bm.GetBlock(blockID)
	if err != nil {
		return nil, err
	}

	x, y, chunkPos := block.GetPosition()

	// Координаты соседей в локальных координатах чанка
	neighborCoords := [][2]int32{
		{x - 1, y},     // лево
		{x + 1, y},     // право
		{x, y - 1},     // верх
		{x, y + 1},     // низ
		{x - 1, y - 1}, // верхний левый
		{x + 1, y - 1}, // верхний правый
		{x - 1, y + 1}, // нижний левый
		{x + 1, y + 1}, // нижний правый
	}

	neighbors := make(map[string]Block)

	for _, coord := range neighborCoords {
		nx, ny := coord[0], coord[1]

		// Определяем позицию чанка и локальные координаты
		nChunkPos := &game.ChunkPosition{X: chunkPos.X, Y: chunkPos.Y}

		// Если блок находится за границей чанка, корректируем координаты
		if nx < 0 {
			nChunkPos.X--
			nx += chunkmanager.ChunkSize
		} else if nx >= chunkmanager.ChunkSize {
			nChunkPos.X++
			nx -= chunkmanager.ChunkSize
		}

		if ny < 0 {
			nChunkPos.Y--
			ny += chunkmanager.ChunkSize
		} else if ny >= chunkmanager.ChunkSize {
			nChunkPos.Y++
			ny -= chunkmanager.ChunkSize
		}

		// Пытаемся получить соседний блок
		neighborID := GetBlockID(nx, ny, nChunkPos)
		neighborBlock, err := bm.GetBlock(neighborID)

		if err == nil {
			neighbors[neighborID] = neighborBlock
		} else {
			// Если блок не найден в кеше, проверяем чанк
			chunk, err := bm.chunkManager.GetOrGenerateChunk(nChunkPos)
			if err != nil {
				continue // Пропускаем в случае ошибки
			}

			// Ищем блок в чанке
			var protoBlock *game.Block
			for _, b := range chunk.Blocks {
				if b.X == nx && b.Y == ny {
					protoBlock = b
					break
				}
			}

			if protoBlock != nil {
				// Создаем блок из прото-объекта
				newBlock, err := bm.CreateBlock(nx, ny, nChunkPos, protoBlock.Type)
				if err == nil {
					// Десериализуем свойства, если они есть
					if len(protoBlock.Properties) > 0 {
						propBytes, _ := json.Marshal(protoBlock.Properties)
						newBlock.Deserialize(propBytes)
					}
					neighbors[neighborID] = newBlock
				}
			}
		}
	}

	return neighbors, nil
}

// UpdateBlock обновляет блок (обычно после изменения свойств)
func (bm *BlockManager) UpdateBlock(block Block) (*game.WorldEvent, error) {
	blockID := block.GetID()

	// Обновляем блок в кеше
	bm.mu.Lock()
	bm.blocks[blockID] = block
	bm.mu.Unlock()

	// Если это динамический блок, обновляем и эту карту
	if dynamicBlock, ok := block.(DynamicBlock); ok {
		bm.dynamicMu.Lock()
		bm.dynamicBlocks[blockID] = dynamicBlock
		bm.dynamicMu.Unlock()
	}

	// Если это интерактивный блок, обновляем и эту карту
	if interactiveBlock, ok := block.(InteractiveBlock); ok {
		bm.interactiveMu.Lock()
		bm.interactiveBlocks[blockID] = interactiveBlock
		bm.interactiveMu.Unlock()
	}

	// Проверяем throttle: для динамических блоков берём MinInterval, для остальных — не ограничиваем
	now := time.Now()
	var minInterval time.Duration
	if db, ok := block.(DynamicBlock); ok {
		minInterval = db.GetScheduleInfo().MinInterval
	}
	bm.mu.Lock()
	if last, ok := bm.lastSent[blockID]; ok && minInterval > 0 && now.Sub(last) < minInterval {
		bm.mu.Unlock()
		// Пропускаем отправку, событие недавно отправлялось
		return nil, nil
	}
	bm.lastSent[blockID] = now
	bm.mu.Unlock()

	// Создаем событие изменения блока
	x, y, chunkPos := block.GetPosition()
	worldX := float32(chunkPos.X*chunkmanager.ChunkSize + x)
	worldY := float32(chunkPos.Y*chunkmanager.ChunkSize + y)

	event := &game.WorldEvent{
		Type: game.WorldEvent_BLOCK_CHANGED,
		Position: &game.Position{
			X: worldX,
			Y: worldY,
			Z: 0,
		},
		Payload: &game.WorldEvent_Block{
			Block: block.ToProto(),
		},
		Message: fmt.Sprintf("Блок %d изменился", block.GetType()),
	}

	// Отправляем событие в канал
	bm.blockEvents <- event

	return event, nil
}

// InteractWithBlock обрабатывает взаимодействие игрока с блоком
func (bm *BlockManager) InteractWithBlock(playerID string, x, y int32, chunkPos *game.ChunkPosition, interactionType string, data map[string]string) (*game.WorldEvent, error) {
	blockID := GetBlockID(x, y, chunkPos)

	bm.interactiveMu.RLock()
	block, exists := bm.interactiveBlocks[blockID]
	bm.interactiveMu.RUnlock()

	if !exists {
		return nil, ErrBlockNotFound
	}

	// Вызываем обработчик взаимодействия
	event, err := block.OnInteract(playerID, interactionType, data)
	if err != nil {
		return nil, err
	}

	// Если блок изменился, обновляем его
	if block.HasChanges() {
		bm.UpdateBlock(block)
	}

	// Отправляем событие в канал, если оно не nil
	if event != nil {
		bm.blockEvents <- event
	}

	return event, nil
}

// RemoveBlock удаляет блок из менеджера
func (bm *BlockManager) RemoveBlock(blockID string) {
	bm.mu.Lock()
	delete(bm.blocks, blockID)
	bm.mu.Unlock()

	bm.dynamicMu.Lock()
	delete(bm.dynamicBlocks, blockID)
	bm.dynamicMu.Unlock()

	bm.interactiveMu.Lock()
	delete(bm.interactiveBlocks, blockID)
	bm.interactiveMu.Unlock()
}

// scheduleBlockUpdate добавляет блок в очередь обновлений
func (bm *BlockManager) scheduleBlockUpdate(blockID string, nextTick time.Time, priority int) {
	bm.queueMu.Lock()
	defer bm.queueMu.Unlock()

	// Добавляем информацию об обновлении
	updateInfo := &blockUpdateInfo{
		BlockID:  blockID,
		NextTick: nextTick,
		Priority: priority,
	}

	bm.updateQueue = append(bm.updateQueue, updateInfo)

	// Сортируем очередь по времени и приоритету
	sort.Slice(bm.updateQueue, func(i, j int) bool {
		if bm.updateQueue[i].NextTick.Equal(bm.updateQueue[j].NextTick) {
			return bm.updateQueue[i].Priority > bm.updateQueue[j].Priority
		}
		return bm.updateQueue[i].NextTick.Before(bm.updateQueue[j].NextTick)
	})
}

// StartBlockUpdateLoop запускает цикл обновления блоков
func (bm *BlockManager) StartBlockUpdateLoop(ctx context.Context) {
	ticker := time.NewTicker(bm.tickingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			bm.processDynamicBlocks()
		}
	}
}

// GetBlockEvents возвращает канал событий изменения блоков
func (bm *BlockManager) GetBlockEvents() <-chan *game.WorldEvent {
	return bm.blockEvents
}

// processDynamicBlocks обрабатывает очередь обновлений динамических блоков
func (bm *BlockManager) processDynamicBlocks() {
	now := time.Now()

	// Получаем блоки для обновления
	bm.queueMu.Lock()

	// Находим индекс последнего блока, который нужно обновить
	updateIndex := -1
	for i, update := range bm.updateQueue {
		if update.NextTick.After(now) {
			break
		}
		updateIndex = i
	}

	// Если нет блоков для обновления, выходим
	if updateIndex == -1 {
		bm.queueMu.Unlock()
		return
	}

	// Получаем блоки для обновления и удаляем их из очереди
	blocksToUpdate := make([]string, updateIndex+1)
	for i := 0; i <= updateIndex; i++ {
		blocksToUpdate[i] = bm.updateQueue[i].BlockID
	}

	// Удаляем обработанные блоки из очереди
	bm.updateQueue = bm.updateQueue[updateIndex+1:]

	bm.queueMu.Unlock()

	// Обрабатываем каждый блок
	for _, blockID := range blocksToUpdate {
		bm.dynamicMu.RLock()
		block, exists := bm.dynamicBlocks[blockID]
		bm.dynamicMu.RUnlock()

		if !exists {
			continue
		}

		// Получаем соседей блока
		neighbors, err := bm.GetNeighborBlocks(blockID)
		if err != nil {
			continue
		}

		// Обновляем блок
		changed := block.Tick(bm.tickingInterval, neighbors)

		// Если блок изменился, создаем событие
		if changed {
			bm.UpdateBlock(block)
		}

		// Для огня: пробуем распространить его на соседа
		if fb, ok := block.(*FireBlock); ok {
			if fb.rnd.Intn(100) < fb.spreadChance {
				// Выбираем одного случайного соседа
				if len(neighbors) > 0 {
					var nb Block
					for _, b := range neighbors {
						nb = b
						break
					}

					// Если соседний блок - огненный, пропускаем
					if nb.GetType() == BlockTypeFire {
						return // Заглушка, чтобы огонь не распространялся на огненные блоки
					}

					x2, y2, chunkPos2 := nb.GetPosition()
					// Создаем новый огненный блок и отправляем событие о размещении
					newFire, err := bm.CreateBlock(x2, y2, chunkPos2, BlockTypeFire)
					if err == nil {
						// Отправляем BLOCK_PLACED событие
						posX := float32(chunkPos2.X*chunkmanager.ChunkSize + x2)
						posY := float32(chunkPos2.Y*chunkmanager.ChunkSize + y2)
						event := &game.WorldEvent{
							Type:     game.WorldEvent_BLOCK_PLACED,
							Position: &game.Position{X: posX, Y: posY, Z: 0},
							Payload:  &game.WorldEvent_Block{Block: newFire.ToProto()},
							Message:  fmt.Sprintf("Огонь распространился на [%d,%d]", x2, y2),
						}
						bm.blockEvents <- event
						// Записываем новый блок в чанк для сохранения и отображения
						_ = bm.chunkManager.SetBlock(chunkPos2, newFire.ToProto())
					}
				}
			}
		}

		// Планируем следующее обновление
		scheduleInfo := block.GetScheduleInfo()
		bm.scheduleBlockUpdate(blockID, scheduleInfo.NextTick, scheduleInfo.Priority)
	}
}

// LoadBlocksFromChunk загружает блоки из чанка в менеджер блоков
func (bm *BlockManager) LoadBlocksFromChunk(chunk *game.Chunk) error {
	for _, protoBlock := range chunk.Blocks {
		// Создаем блок
		block, err := bm.CreateBlock(protoBlock.X, protoBlock.Y, chunk.Position, protoBlock.Type)
		if err != nil {
			continue // Пропускаем блок в случае ошибки
		}

		// Десериализуем свойства, если они есть
		if len(protoBlock.Properties) > 0 {
			propBytes, _ := json.Marshal(protoBlock.Properties)
			block.Deserialize(propBytes)
		}

		// Сбрасываем флаг изменений, так как блок только загружен
		block.ResetChanges()

		// Если блок динамический, отмечаем чанк как активный
		if dyn, ok := block.(DynamicBlock); ok {
			_ = dyn // use dynamic block, state already loaded
			bm.chunkManager.MarkChunkAsActive(chunk.Position)
		}
	}

	return nil
}

// SaveBlocksToChunk сохраняет блоки из менеджера в чанк
func (bm *BlockManager) SaveBlocksToChunk(chunk *game.Chunk) error {
	// Очищаем существующие блоки в чанке
	chunk.Blocks = make([]*game.Block, 0)

	// Получаем все блоки для этого чанка
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	for _, block := range bm.blocks {
		_, _, blockChunkPos := block.GetPosition()

		// Если блок принадлежит этому чанку
		if blockChunkPos.X == chunk.Position.X && blockChunkPos.Y == chunk.Position.Y {
			// Добавляем блок в чанк
			chunk.Blocks = append(chunk.Blocks, block.ToProto())
		}
	}

	return nil
}

// GetChunkActiveStatus проверяет, содержит ли чанк активные блоки
func (bm *BlockManager) GetChunkActiveStatus(chunkPos *game.ChunkPosition) bool {
	bm.dynamicMu.RLock()
	defer bm.dynamicMu.RUnlock()

	for _, block := range bm.dynamicBlocks {
		_, _, blockChunkPos := block.GetPosition()
		if blockChunkPos.X == chunkPos.X && blockChunkPos.Y == chunkPos.Y {
			return true
		}
	}

	return false
}

// Создание статических блоков
func NewStaticBlock(x, y int32, chunkPos *game.ChunkPosition, blockType int32) Block {
	return NewBaseBlock(x, y, chunkPos, blockType)
}
