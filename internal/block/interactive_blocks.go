package block

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// Константы для интерактивных блоков
const (
	// Типы интерактивных блоков (расширение списка из dynamic_blocks.go)
	BlockTypeDoor    = 30 // Дверь
	BlockTypeChest   = 31 // Сундук
	BlockTypeFurnace = 32 // Печь

	// Свойства двери
	DoorPropertyState  = "state"  // Состояние двери ("open", "closed")
	DoorPropertyLocked = "locked" // Заблокирована ли дверь ("true", "false")
	DoorPropertyOwner  = "owner"  // ID владельца двери

	// Свойства сундука
	ChestPropertyContents = "contents" // Содержимое сундука (JSON-строка с массивом предметов)
	ChestPropertyOwner    = "owner"    // ID владельца сундука
	ChestPropertyLocked   = "locked"   // Заблокирован ли сундук ("true", "false")

	// Типы взаимодействия
	InteractionTypeUse     = "use"     // Использовать блок (открыть дверь, открыть сундук)
	InteractionTypeLock    = "lock"    // Заблокировать блок
	InteractionTypeUnlock  = "unlock"  // Разблокировать блок
	InteractionTypePutItem = "putItem" // Положить предмет (в сундук)
	InteractionTypeGetItem = "getItem" // Взять предмет (из сундука)
)

// DoorBlock представляет интерактивную дверь
type DoorBlock struct {
	*BaseBlock
	isOpen   bool   // Открыта ли дверь
	isLocked bool   // Заблокирована ли дверь
	owner    string // ID владельца двери
}

// NewDoorBlock создает новую дверь
func NewDoorBlock(x, y int32, chunkPos *game.ChunkPosition, blockType int32) Block {
	baseBlock := NewBaseBlock(x, y, chunkPos, BlockTypeDoor)

	door := &DoorBlock{
		BaseBlock: baseBlock,
		isOpen:    false,
		isLocked:  false,
		owner:     "",
	}

	// Устанавливаем начальные свойства
	door.SetProperty(DoorPropertyState, "closed")
	door.SetProperty(DoorPropertyLocked, "false")

	return door
}

// Tick обновляет состояние двери (для анимации или других эффектов)
func (d *DoorBlock) Tick(dt time.Duration, neighbors map[string]Block) bool {
	// Двери не требуют регулярного обновления, но могут, например,
	// автоматически закрываться через некоторое время
	return false
}

// GetScheduleInfo возвращает информацию о планировании обновлений
func (d *DoorBlock) GetScheduleInfo() TickScheduleInfo {
	return TickScheduleInfo{
		MinInterval: 1 * time.Minute, // Двери редко требуют обновления
		Priority:    2,
		NextTick:    time.Now().Add(1 * time.Minute),
	}
}

// OnInteract обрабатывает взаимодействие игрока с дверью
func (d *DoorBlock) OnInteract(playerID string, interactionType string, data map[string]string) (*game.WorldEvent, error) {
	switch interactionType {
	case InteractionTypeUse:
		// Проверяем, заблокирована ли дверь
		if d.isLocked {
			// Если дверь заблокирована, только владелец может её открыть
			if d.owner != playerID {
				return nil, fmt.Errorf("дверь заблокирована")
			}
		}

		// Изменяем состояние двери
		d.isOpen = !d.isOpen

		// Обновляем свойства
		state := "closed"
		if d.isOpen {
			state = "open"
		}
		d.SetProperty(DoorPropertyState, state)

		// Создаем событие для клиентов
		x, y, chunkPos := d.GetPosition()
		worldX := float32(chunkPos.X*16 + x)
		worldY := float32(chunkPos.Y*16 + y)

		return &game.WorldEvent{
			Type: game.WorldEvent_BLOCK_PLACED, // Используем существующий тип
			Position: &game.Position{
				X: worldX,
				Y: worldY,
				Z: 0,
			},
			PlayerId: playerID,
			Message:  fmt.Sprintf("Дверь %s", state),
			Payload: &game.WorldEvent_Block{
				Block: d.ToProto(),
			},
		}, nil

	case InteractionTypeLock:
		// Только владелец может блокировать дверь
		if d.owner != "" && d.owner != playerID {
			return nil, fmt.Errorf("только владелец может заблокировать дверь")
		}

		// Если еще нет владельца, устанавливаем его
		if d.owner == "" {
			d.owner = playerID
			d.SetProperty(DoorPropertyOwner, playerID)
		}

		// Блокируем дверь
		d.isLocked = true
		d.SetProperty(DoorPropertyLocked, "true")

		// Получаем позицию
		x, y, chunkPos := d.GetPosition()

		return &game.WorldEvent{
			Type: game.WorldEvent_BLOCK_PLACED, // Используем существующий тип
			Position: &game.Position{
				X: float32(chunkPos.X*16 + x),
				Y: float32(chunkPos.Y*16 + y),
				Z: 0,
			},
			PlayerId: playerID,
			Message:  "Дверь заблокирована",
			Payload: &game.WorldEvent_Block{
				Block: d.ToProto(),
			},
		}, nil

	case InteractionTypeUnlock:
		// Только владелец может разблокировать дверь
		if d.owner != playerID {
			return nil, fmt.Errorf("только владелец может разблокировать дверь")
		}

		// Разблокируем дверь
		d.isLocked = false
		d.SetProperty(DoorPropertyLocked, "false")

		// Получаем позицию
		x, y, chunkPos := d.GetPosition()

		return &game.WorldEvent{
			Type: game.WorldEvent_BLOCK_INTERACTION,
			Position: &game.Position{
				X: float32(chunkPos.X*16 + x),
				Y: float32(chunkPos.Y*16 + y),
				Z: 0,
			},
			PlayerId: playerID,
			Message:  "Дверь разблокирована",
			Payload: &game.WorldEvent_Block{
				Block: d.ToProto(),
			},
		}, nil

	default:
		return nil, fmt.Errorf("неизвестный тип взаимодействия: %s", interactionType)
	}
}

// ToProto преобразует блок в протобуф-сообщение
func (d *DoorBlock) ToProto() *game.Block {
	protoBlock := d.BaseBlock.ToProto()

	// Используем свойства для хранения информации о блоке
	protoBlock.Properties["_isDynamic"] = "true"
	protoBlock.Properties["_isInteractive"] = "true"

	return protoBlock
}

// Структура для представления предмета в инвентаре
type ChestItem struct {
	ItemID   int    `json:"item_id"`  // ID предмета
	Quantity int    `json:"quantity"` // Количество
	Name     string `json:"name"`     // Название
	Data     string `json:"data"`     // Дополнительные данные
}

// ChestBlock представляет сундук
type ChestBlock struct {
	*BaseBlock
	items    []ChestItem // Предметы в сундуке
	isLocked bool        // Заблокирован ли сундук
	owner    string      // ID владельца сундука
}

// NewChestBlock создает новый сундук
func NewChestBlock(x, y int32, chunkPos *game.ChunkPosition, blockType int32) Block {
	baseBlock := NewBaseBlock(x, y, chunkPos, BlockTypeChest)

	chest := &ChestBlock{
		BaseBlock: baseBlock,
		items:     make([]ChestItem, 0),
		isLocked:  false,
	}

	// Устанавливаем начальные свойства
	chest.SetProperty(ChestPropertyLocked, "false")
	chest.setContentsProperty()

	return chest
}

// setContentsProperty сериализует содержимое сундука в свойство
func (c *ChestBlock) setContentsProperty() error {
	itemsJSON, err := json.Marshal(c.items)
	if err != nil {
		return err
	}

	c.SetProperty(ChestPropertyContents, string(itemsJSON))
	return nil
}

// loadContentsFromProperty загружает содержимое сундука из свойства
func (c *ChestBlock) loadContentsFromProperty() error {
	contentsJSON, exists := c.GetProperty(ChestPropertyContents)
	if !exists || contentsJSON == "" {
		c.items = make([]ChestItem, 0)
		return nil
	}

	return json.Unmarshal([]byte(contentsJSON), &c.items)
}

// Tick обновляет состояние сундука
func (c *ChestBlock) Tick(dt time.Duration, neighbors map[string]Block) bool {
	// Сундуки обычно не требуют регулярного обновления
	return false
}

// GetScheduleInfo возвращает информацию о планировании обновлений
func (c *ChestBlock) GetScheduleInfo() TickScheduleInfo {
	return TickScheduleInfo{
		MinInterval: 10 * time.Minute, // Сундуки очень редко требуют обновления
		Priority:    1,
		NextTick:    time.Now().Add(10 * time.Minute),
	}
}

// OnInteract обрабатывает взаимодействие игрока с сундуком
func (c *ChestBlock) OnInteract(playerID string, interactionType string, data map[string]string) (*game.WorldEvent, error) {
	// Загружаем текущее содержимое
	if err := c.loadContentsFromProperty(); err != nil {
		return nil, err
	}

	x, y, chunkPos := c.GetPosition()
	worldX := float32(chunkPos.X*16 + x)
	worldY := float32(chunkPos.Y*16 + y)
	worldPos := &game.Position{X: worldX, Y: worldY, Z: 0}

	// Проверяем, заблокирован ли сундук
	if c.isLocked && interactionType != InteractionTypeUnlock && c.owner != playerID {
		return nil, fmt.Errorf("сундук заблокирован")
	}

	switch interactionType {
	case InteractionTypeUse:
		// Просто открываем сундук для просмотра содержимого
		// В реальной системе здесь бы отправлялся интерфейс инвентаря
		message := fmt.Sprintf("Сундук открыт, содержимое: %d предметов", len(c.items))

		return &game.WorldEvent{
			Type:     game.WorldEvent_BLOCK_PLACED, // Используем существующий тип
			Position: worldPos,
			PlayerId: playerID,
			Message:  message,
			Payload:  &game.WorldEvent_Block{Block: c.ToProto()},
		}, nil

	case InteractionTypeLock:
		// Блокируем сундук, как с дверью
		if c.owner != "" && c.owner != playerID {
			return nil, fmt.Errorf("только владелец может заблокировать сундук")
		}

		if c.owner == "" {
			c.owner = playerID
			c.SetProperty(ChestPropertyOwner, playerID)
		}

		c.isLocked = true
		c.SetProperty(ChestPropertyLocked, "true")

		return &game.WorldEvent{
			Type:     game.WorldEvent_BLOCK_INTERACTION,
			Position: worldPos,
			PlayerId: playerID,
			Message:  "Сундук заблокирован",
			Payload:  &game.WorldEvent_Block{Block: c.ToProto()},
		}, nil

	case InteractionTypeUnlock:
		if c.owner != playerID {
			return nil, fmt.Errorf("только владелец может разблокировать сундук")
		}

		c.isLocked = false
		c.SetProperty(ChestPropertyLocked, "false")

		return &game.WorldEvent{
			Type:     game.WorldEvent_BLOCK_INTERACTION,
			Position: worldPos,
			PlayerId: playerID,
			Message:  "Сундук разблокирован",
			Payload:  &game.WorldEvent_Block{Block: c.ToProto()},
		}, nil

	case InteractionTypePutItem:
		// Добавляем предмет в сундук
		itemID, exists := data["item_id"]
		if !exists {
			return nil, fmt.Errorf("не указан ID предмета")
		}

		// Преобразуем строку в число
		itemIDInt, err := strconv.Atoi(itemID)
		if err != nil {
			return nil, fmt.Errorf("неверный формат ID предмета: %w", err)
		}

		// Получаем количество
		quantityStr, exists := data["quantity"]
		quantity := 1
		if exists {
			quantity, err = strconv.Atoi(quantityStr)
			if err != nil || quantity <= 0 {
				quantity = 1
			}
		}

		// Получаем название предмета
		name := data["name"]
		if name == "" {
			name = fmt.Sprintf("Предмет #%d", itemIDInt)
		}

		// Добавляем предмет
		c.items = append(c.items, ChestItem{
			ItemID:   itemIDInt,
			Quantity: quantity,
			Name:     name,
			Data:     data["item_data"],
		})

		// Обновляем свойство с содержимым
		c.setContentsProperty()

		return &game.WorldEvent{
			Type:     game.WorldEvent_BLOCK_INTERACTION,
			Position: worldPos,
			PlayerId: playerID,
			Message:  fmt.Sprintf("Предмет '%s' добавлен в сундук", name),
			Payload:  &game.WorldEvent_Block{Block: c.ToProto()},
		}, nil

	case InteractionTypeGetItem:
		// Извлекаем предмет из сундука
		// Получаем индекс предмета
		indexStr, exists := data["index"]
		if !exists {
			return nil, fmt.Errorf("не указан индекс предмета")
		}

		index, err := strconv.Atoi(indexStr)
		if err != nil || index < 0 || index >= len(c.items) {
			return nil, fmt.Errorf("неверный индекс предмета")
		}

		// Получаем предмет
		item := c.items[index]

		// Получаем количество для извлечения
		quantityStr, exists := data["quantity"]
		quantity := 1
		if exists {
			quantity, err = strconv.Atoi(quantityStr)
			if err != nil || quantity <= 0 {
				quantity = 1
			}
		}

		// Проверяем, что у нас достаточно предметов
		if quantity > item.Quantity {
			quantity = item.Quantity
		}

		// Уменьшаем количество или удаляем предмет
		if quantity == item.Quantity {
			// Удаляем предмет полностью
			c.items = append(c.items[:index], c.items[index+1:]...)
		} else {
			// Уменьшаем количество
			c.items[index].Quantity -= quantity
		}

		// Обновляем свойство с содержимым
		c.setContentsProperty()

		return &game.WorldEvent{
			Type:     game.WorldEvent_BLOCK_INTERACTION,
			Position: worldPos,
			PlayerId: playerID,
			Message:  fmt.Sprintf("Получено %d шт. предмета '%s' из сундука", quantity, item.Name),
			Payload:  &game.WorldEvent_Block{Block: c.ToProto()},
		}, nil

	default:
		return nil, fmt.Errorf("неизвестный тип взаимодействия: %s", interactionType)
	}
}

// ToProto преобразует блок в протобуф-сообщение
func (c *ChestBlock) ToProto() *game.Block {
	protoBlock := c.BaseBlock.ToProto()

	// Используем свойства для хранения информации о блоке
	protoBlock.Properties["_isDynamic"] = "true"
	protoBlock.Properties["_isInteractive"] = "true"

	return protoBlock
}

// Deserialize десериализует свойства сундука
func (c *ChestBlock) Deserialize(data []byte) error {
	// Вызываем десериализацию базового блока
	if err := c.BaseBlock.Deserialize(data); err != nil {
		return err
	}

	// Загружаем дополнительные поля из свойств
	if lockedStr, exists := c.GetProperty(ChestPropertyLocked); exists {
		c.isLocked = strings.ToLower(lockedStr) == "true"
	}

	if owner, exists := c.GetProperty(ChestPropertyOwner); exists {
		c.owner = owner
	}

	// Загружаем содержимое
	return c.loadContentsFromProperty()
}

// RegisterInteractiveBlocks регистрирует фабрики для интерактивных блоков
func RegisterInteractiveBlocks(manager *BlockManager) {
	manager.RegisterBlockFactory(BlockTypeDoor, NewDoorBlock)
	manager.RegisterBlockFactory(BlockTypeChest, NewChestBlock)
}
