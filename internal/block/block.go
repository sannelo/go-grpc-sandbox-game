package block

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// Интерфейс Block определяет базовое поведение всех блоков
type Block interface {
	// GetID возвращает уникальный ID блока (x:y:чанк_x:чанк_y)
	GetID() string

	// GetPosition возвращает позицию блока
	GetPosition() (int32, int32, *game.ChunkPosition)

	// GetType возвращает тип блока
	GetType() int32

	// Serialize сериализует состояние блока в байты
	Serialize() ([]byte, error)

	// Deserialize десериализует состояние блока из байтов
	Deserialize(data []byte) error

	// ToProto преобразует блок в протобуф-сообщение
	ToProto() *game.Block

	// HasChanges возвращает true, если состояние блока изменилось с момента последней синхронизации
	HasChanges() bool

	// ResetChanges сбрасывает флаг изменений после синхронизации
	ResetChanges()
}

// DynamicBlock расширяет Block для блоков, которые могут изменять состояние со временем
type DynamicBlock interface {
	Block

	// Tick обновляет состояние блока (вызывается игровым циклом)
	Tick(dt time.Duration, neighbors map[string]Block) bool

	// GetScheduleInfo возвращает информацию о том, как часто блок должен обновляться
	GetScheduleInfo() TickScheduleInfo
}

// InteractiveBlock расширяет DynamicBlock для блоков, с которыми игрок может взаимодействовать
type InteractiveBlock interface {
	DynamicBlock

	// OnInteract обрабатывает взаимодействие игрока с блоком
	OnInteract(playerID string, interactionType string, data map[string]string) (*game.WorldEvent, error)
}

// TickScheduleInfo определяет параметры обновления блока
type TickScheduleInfo struct {
	// MinInterval указывает минимальное время между обновлениями (мсек)
	MinInterval time.Duration

	// Priority определяет приоритет обновления (выше число - выше приоритет)
	Priority int

	// NextTick указывает, когда блок должен получить следующее обновление
	NextTick time.Time
}

// BaseBlock предоставляет базовую реализацию для всех блоков
type BaseBlock struct {
	X         int32               // Локальная X-координата в чанке
	Y         int32               // Локальная Y-координата в чанке
	ChunkPos  *game.ChunkPosition // Позиция чанка
	BlockType int32               // Тип блока

	Properties map[string]string // Свойства блока
	changed    bool              // Флаг изменения состояния
	mu         sync.RWMutex      // Мьютекс для безопасного доступа к состоянию
}

// NewBaseBlock создает новый базовый блок
func NewBaseBlock(x, y int32, chunkPos *game.ChunkPosition, blockType int32) *BaseBlock {
	return &BaseBlock{
		X:          x,
		Y:          y,
		ChunkPos:   chunkPos,
		BlockType:  blockType,
		Properties: make(map[string]string),
		changed:    true, // Изначально помечаем как измененный для первичной синхронизации
	}
}

// GetID возвращает уникальный ID блока
func (b *BaseBlock) GetID() string {
	return GetBlockID(b.X, b.Y, b.ChunkPos)
}

// GetPosition возвращает позицию блока
func (b *BaseBlock) GetPosition() (int32, int32, *game.ChunkPosition) {
	return b.X, b.Y, b.ChunkPos
}

// GetType возвращает тип блока
func (b *BaseBlock) GetType() int32 {
	return b.BlockType
}

// Serialize сериализует состояние блока
func (b *BaseBlock) Serialize() ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return json.Marshal(b.Properties)
}

// Deserialize десериализует состояние блока
func (b *BaseBlock) Deserialize(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	return json.Unmarshal(data, &b.Properties)
}

// ToProto преобразует блок в протобуф-сообщение
func (b *BaseBlock) ToProto() *game.Block {
	b.mu.RLock()
	// Copy properties to avoid sharing live map and concurrent modifications
	propsCopy := make(map[string]string, len(b.Properties))
	for key, val := range b.Properties {
		propsCopy[key] = val
	}
	b.mu.RUnlock()
	// Return new game.Block with copied properties map
	return &game.Block{
		X:          b.X,
		Y:          b.Y,
		Type:       b.BlockType,
		Properties: propsCopy,
	}
}

// HasChanges возвращает true, если состояние блока изменилось
func (b *BaseBlock) HasChanges() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.changed
}

// ResetChanges сбрасывает флаг изменений
func (b *BaseBlock) ResetChanges() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.changed = false
}

// SetProperty устанавливает свойство блока
func (b *BaseBlock) SetProperty(key, value string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.Properties[key] = value
	b.changed = true
}

// GetProperty возвращает значение свойства блока
func (b *BaseBlock) GetProperty(key string) (string, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	val, exists := b.Properties[key]
	return val, exists
}

// MarkChanged помечает блок как измененный
func (b *BaseBlock) MarkChanged() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.changed = true
}

// GetBlockID генерирует ID блока по его координатам
func GetBlockID(x, y int32, chunkPos *game.ChunkPosition) string {
	return fmt.Sprintf("%d:%d:%d:%d", x, y, chunkPos.X, chunkPos.Y)
}

// Ошибки
var (
	ErrInvalidBlockType  = errors.New("неверный тип блока")
	ErrBlockNotFound     = errors.New("блок не найден")
	ErrInteractionFailed = errors.New("взаимодействие с блоком не удалось")
)
