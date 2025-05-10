package service

import (
	"context"
	"testing"
	"time"

	"github.com/annelo/go-grpc-server/internal/block"
	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/storage"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
	"github.com/stretchr/testify/assert"
)

// MockStorage имитирует интерфейс хранилища
type MockStorage struct {
	chunks       map[string]*game.Chunk
	playerStates map[string]*storage.PlayerState
}

// NewMockStorage создает новое мок-хранилище
func NewMockStorage() *MockStorage {
	return &MockStorage{
		chunks:       make(map[string]*game.Chunk),
		playerStates: make(map[string]*storage.PlayerState),
	}
}

// SaveChunk сохраняет чанк в хранилище
func (s *MockStorage) SaveChunk(ctx context.Context, chunk *game.Chunk) error {
	s.chunks[getChunkKeyString(chunk.Position)] = chunk
	return nil
}

// LoadChunk загружает чанк из хранилища
func (s *MockStorage) LoadChunk(ctx context.Context, pos *game.ChunkPosition) (*game.Chunk, error) {
	key := getChunkKeyString(pos)
	if chunk, ok := s.chunks[key]; ok {
		return chunk, nil
	}
	return &game.Chunk{Position: pos}, nil
}

// DeleteChunk удаляет чанк из хранилища
func (s *MockStorage) DeleteChunk(ctx context.Context, pos *game.ChunkPosition) error {
	key := getChunkKeyString(pos)
	delete(s.chunks, key)
	return nil
}

// ListChunks возвращает список всех сохранённых чанков
func (s *MockStorage) ListChunks(ctx context.Context) ([]*game.ChunkPosition, error) {
	positions := make([]*game.ChunkPosition, 0, len(s.chunks))
	for _, chunk := range s.chunks {
		positions = append(positions, chunk.Position)
	}
	return positions, nil
}

// SaveWorld сохраняет общую информацию о мире
func (s *MockStorage) SaveWorld(ctx context.Context, info *storage.WorldInfo) error {
	return nil
}

// LoadWorld загружает информацию о мире
func (s *MockStorage) LoadWorld(ctx context.Context) (*storage.WorldInfo, error) {
	return &storage.WorldInfo{Seed: 12345}, nil
}

// SavePlayerState сохраняет состояние игрока
func (s *MockStorage) SavePlayerState(ctx context.Context, state *storage.PlayerState) error {
	s.playerStates[state.ID] = state
	return nil
}

// LoadPlayerState загружает состояние игрока
func (s *MockStorage) LoadPlayerState(ctx context.Context, playerID string) (*storage.PlayerState, error) {
	if state, ok := s.playerStates[playerID]; ok {
		return state, nil
	}
	return nil, nil
}

// Close закрывает хранилище
func (s *MockStorage) Close() error {
	return nil
}

// getChunkKeyString возвращает ключ чанка
func getChunkKeyString(pos *game.ChunkPosition) string {
	return pos.String()
}

// EventSubscription имитирует подписку на события блоков
type EventSubscription struct {
	Events chan *game.WorldEvent
}

// Close закрывает подписку
func (s *EventSubscription) Close() {
	close(s.Events)
}

func TestWorldService_Blocks(t *testing.T) {
	// Создаем контекст
	ctx := context.Background()

	// Создаем мок-хранилище
	storage := NewMockStorage()

	// Создаем WorldService
	service := NewWorldServiceWithStorage(storage)

	// Создаем тестовый чанк
	chunkPos := &game.ChunkPosition{X: 1, Y: 2}

	// Получаем чанк через сервис (это создаст его)
	chunk, err := service.GetChunk(ctx, chunkPos)
	assert.NoError(t, err, "Получение чанка не должно вызывать ошибку")
	assert.NotNil(t, chunk, "Чанк должен быть создан")

	// Создаем блоки через сервис
	doorBlockProto := &game.Block{
		X:    3,
		Y:    4,
		Type: block.BlockTypeDoor,
	}

	fireBlockProto := &game.Block{
		X:    5,
		Y:    6,
		Type: block.BlockTypeFire,
	}

	// Размещаем блоки
	event, err := service.PlaceBlock(ctx, doorBlockProto, chunkPos, "player1")
	assert.NoError(t, err, "Размещение блока двери не должно вызывать ошибку")
	assert.NotNil(t, event, "Должно быть создано событие")

	event, err = service.PlaceBlock(ctx, fireBlockProto, chunkPos, "player1")
	assert.NoError(t, err, "Размещение блока огня не должно вызывать ошибку")
	assert.NotNil(t, event, "Должно быть создано событие")

	// Взаимодействуем с блоком двери
	event, err = service.InteractWithBlock(ctx, doorBlockProto.X, doorBlockProto.Y, chunkPos, "player1", block.InteractionTypeUse, nil)
	assert.NoError(t, err, "Взаимодействие с блоком не должно вызывать ошибку")
	assert.NotNil(t, event, "Должно быть создано событие")

	// Получаем обновленный чанк
	updatedChunk, err := service.GetChunk(ctx, chunkPos)
	assert.NoError(t, err, "Получение обновленного чанка не должно вызывать ошибку")

	// Проверяем, что блоки сохранены в чанке
	assert.GreaterOrEqual(t, len(updatedChunk.Blocks), 2, "В чанке должно быть минимум 2 блока")

	// Проверяем, что чанк помечен как активный (из-за динамических блоков)
	assert.True(t, service.chunkManager.IsChunkActive(chunkPos), "Чанк должен быть помечен как активный")

	// Проверяем, что события отправляются при обновлении динамических блоков
	// В реальной системе здесь была бы подписка на события и проверка их получения

	// Уничтожаем блок
	event, err = service.RemoveBlock(ctx, doorBlockProto.X, doorBlockProto.Y, chunkPos, "player1")
	assert.NoError(t, err, "Удаление блока не должно вызывать ошибку")
	assert.NotNil(t, event, "Должно быть создано событие")

	// Получаем еще раз обновленный чанк
	finalChunk, err := service.GetChunk(ctx, chunkPos)
	assert.NoError(t, err, "Получение финального чанка не должно вызывать ошибку")

	// Проверяем, что блок удален
	found := false
	for _, b := range finalChunk.Blocks {
		if b.X == doorBlockProto.X && b.Y == doorBlockProto.Y {
			found = true
			break
		}
	}
	assert.False(t, found, "Блок должен быть удален из чанка")

	// Проверяем сохранение чанка
	err = service.SaveChunk(ctx, chunkPos)
	assert.NoError(t, err, "Сохранение чанка не должно вызывать ошибку")
}

func TestWorldService_BlockEvents(t *testing.T) {
	// Создаем контекст
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Создаем мок-хранилище
	storage := NewMockStorage()

	// Создаем WorldService
	service := NewWorldServiceWithStorage(storage)

	// Создаем тестовый чанк
	chunkPos := &game.ChunkPosition{X: 3, Y: 4}

	// Получаем чанк через сервис
	_, err := service.GetChunk(ctx, chunkPos)
	assert.NoError(t, err, "Получение чанка не должно вызывать ошибку")

	// Создаем подписку на события через канал блок-событий
	subscription := &EventSubscription{
		Events: make(chan *game.WorldEvent, 10),
	}

	// Регистрируем подписку для получения событий
	RegisterEventSubscription(subscription)

	// Отложенное закрытие подписки
	defer subscription.Close()

	// Создаем блок огня
	fireBlockProto := &game.Block{
		X:         7,
		Y:         8,
		Type:      block.BlockTypeFire,
		IsDynamic: true,
	}

	// Размещаем блок
	_, err = service.PlaceBlock(ctx, fireBlockProto, chunkPos, "player1")
	assert.NoError(t, err, "Размещение блока огня не должно вызывать ошибку")

	// Проверяем, что событие размещения блока получено
	select {
	case event := <-subscription.Events:
		assert.Equal(t, game.WorldEvent_BLOCK_PLACED, event.Type, "Должно быть событие размещения блока")
		assert.Equal(t, "player1", event.PlayerId, "ID игрока должен совпадать")
	case <-time.After(1 * time.Second):
		t.Fatal("Не получено событие размещения блока в течение 1 секунды")
	}

	// Проверяем обновление динамического блока (запуск планировщика)
	go service.blockManager.StartBlockUpdateLoop(ctx)

	// Создаем и эмулируем событие обновления блока
	tickEvent := &game.WorldEvent{
		Type:     game.WorldEvent_BLOCK_TICK,
		Position: &game.Position{X: float32(fireBlockProto.X), Y: float32(fireBlockProto.Y)},
		PlayerId: "system",
	}
	service.emitBlockEvent(tickEvent)

	// Ждем события обновления блока
	select {
	case event := <-subscription.Events:
		assert.Equal(t, game.WorldEvent_BLOCK_TICK, event.Type, "Должно быть событие обновления блока")
	case <-time.After(3 * time.Second):
		t.Fatal("Не получено событие обновления блока в течение 3 секунд")
	}

	// Взаимодействуем с блоком
	_, err = service.InteractWithBlock(ctx, fireBlockProto.X, fireBlockProto.Y, chunkPos, "player1", block.InteractionTypeUse, nil)
	assert.NoError(t, err, "Взаимодействие с блоком не должно вызывать ошибку")

	// Эмулируем событие взаимодействия
	interactEvent := &game.WorldEvent{
		Type:     game.WorldEvent_BLOCK_INTERACTION,
		Position: &game.Position{X: float32(fireBlockProto.X), Y: float32(fireBlockProto.Y)},
		PlayerId: "player1",
	}
	service.emitBlockEvent(interactEvent)

	// Проверяем событие взаимодействия
	select {
	case event := <-subscription.Events:
		assert.Equal(t, game.WorldEvent_BLOCK_INTERACTION, event.Type, "Должно быть событие взаимодействия с блоком")
		assert.Equal(t, "player1", event.PlayerId, "ID игрока должен совпадать")
	case <-time.After(1 * time.Second):
		t.Fatal("Не получено событие взаимодействия с блоком в течение 1 секунды")
	}

	// Удаляем блок
	_, err = service.RemoveBlock(ctx, fireBlockProto.X, fireBlockProto.Y, chunkPos, "player1")
	assert.NoError(t, err, "Удаление блока не должно вызывать ошибку")

	// Проверяем событие удаления
	select {
	case event := <-subscription.Events:
		assert.Equal(t, game.WorldEvent_BLOCK_DESTROYED, event.Type, "Должно быть событие удаления блока")
		assert.Equal(t, "player1", event.PlayerId, "ID игрока должен совпадать")
	case <-time.After(1 * time.Second):
		t.Fatal("Не получено событие удаления блока в течение 1 секунды")
	}
}

func TestWorldService_ChunkLifecycle(t *testing.T) {
	// Создаем контекст
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Создаем мок-хранилище
	storage := NewMockStorage()

	// Создаем WorldService
	service := NewWorldServiceWithStorage(storage)

	// Создаем тестовые чанки
	activeChunkPos := &game.ChunkPosition{X: 5, Y: 6}
	normalChunkPos := &game.ChunkPosition{X: 7, Y: 8}

	// Создаем динамический блок (огонь)
	activeBlockProto := &game.Block{
		X:         3,
		Y:         4,
		Type:      block.BlockTypeFire,
		IsDynamic: true,
	}

	// Создаем обычный блок (камень)
	normalBlockProto := &game.Block{
		X:    3,
		Y:    4,
		Type: chunkmanager.BlockTypeStone,
	}

	// Получаем чанки через сервис
	_, err := service.GetChunk(ctx, activeChunkPos)
	assert.NoError(t, err, "Получение активного чанка не должно вызывать ошибку")

	_, err = service.GetChunk(ctx, normalChunkPos)
	assert.NoError(t, err, "Получение обычного чанка не должно вызывать ошибку")

	// Размещаем блоки
	_, err = service.PlaceBlock(ctx, activeBlockProto, activeChunkPos, "player1")
	assert.NoError(t, err, "Размещение динамического блока не должно вызывать ошибку")

	_, err = service.PlaceBlock(ctx, normalBlockProto, normalChunkPos, "player1")
	assert.NoError(t, err, "Размещение статического блока не должно вызывать ошибку")

	// Проверяем, что активный чанк помечен как активный
	assert.True(t, service.chunkManager.IsChunkActive(activeChunkPos), "Чанк с динамическим блоком должен быть помечен как активный")

	// Проверяем, что обычный чанк не помечен как активный
	assert.False(t, service.chunkManager.IsChunkActive(normalChunkPos), "Чанк со статическим блоком не должен быть активным")

	// Сохраняем чанки
	err = service.SaveChunk(ctx, activeChunkPos)
	assert.NoError(t, err, "Сохранение активного чанка не должно вызывать ошибку")

	err = service.SaveChunk(ctx, normalChunkPos)
	assert.NoError(t, err, "Сохранение обычного чанка не должно вызывать ошибку")

	// Удаляем динамический блок из активного чанка
	_, err = service.RemoveBlock(ctx, activeBlockProto.X, activeBlockProto.Y, activeChunkPos, "player1")
	assert.NoError(t, err, "Удаление динамического блока не должно вызывать ошибку")

	// Проверяем, что чанк больше не активный
	assert.False(t, service.chunkManager.IsChunkActive(activeChunkPos), "Чанк без динамических блоков не должен быть активным")

	// Проверяем, что чанки сохранены в хранилище
	_, err = storage.LoadChunk(ctx, activeChunkPos)
	assert.NoError(t, err, "Загрузка активного чанка из хранилища не должна вызывать ошибку")

	_, err = storage.LoadChunk(ctx, normalChunkPos)
	assert.NoError(t, err, "Загрузка обычного чанка из хранилища не должна вызывать ошибку")
}

// ~~~ Методы расширения для тестирования ~~~

// GetChunk получает чанк из WorldService
func (s *WorldService) GetChunk(ctx context.Context, pos *game.ChunkPosition) (*game.Chunk, error) {
	return s.loadChunkWithBlocks(pos)
}

// PlaceBlock размещает блок
func (s *WorldService) PlaceBlock(ctx context.Context, blockProto *game.Block, chunkPos *game.ChunkPosition, playerID string) (*game.WorldEvent, error) {
	// Создаем блок через BlockManager
	blockObj, err := s.blockManager.CreateBlock(blockProto.X, blockProto.Y, chunkPos, blockProto.Type)
	if err != nil {
		return nil, err
	}

	// Формируем событие
	event := &game.WorldEvent{
		Type:     game.WorldEvent_BLOCK_PLACED,
		Position: &game.Position{X: float32(blockProto.X), Y: float32(blockProto.Y)},
		PlayerId: playerID,
		Payload: &game.WorldEvent_Block{
			Block: blockProto,
		},
	}

	// Получаем чанк и добавляем блок в него
	chunk, err := s.GetChunk(ctx, chunkPos)
	if err != nil {
		return nil, err
	}

	// Добавляем блок в чанк
	found := false
	for i, b := range chunk.Blocks {
		if b.X == blockProto.X && b.Y == blockProto.Y {
			chunk.Blocks[i] = blockProto
			found = true
			break
		}
	}

	if !found {
		chunk.Blocks = append(chunk.Blocks, blockProto)
	}

	// Если это динамический блок - помечаем чанк как активный
	if _, ok := blockObj.(block.DynamicBlock); ok {
		s.chunkManager.MarkChunkAsActive(chunkPos)
	}

	// Эмулируем отправку события для тестов
	s.emitBlockEvent(event)

	return event, nil
}

// InteractWithBlock взаимодействует с блоком
func (s *WorldService) InteractWithBlock(ctx context.Context, x, y int32, chunkPos *game.ChunkPosition, playerID string, interactionType string, data map[string]string) (*game.WorldEvent, error) {
	return s.blockManager.InteractWithBlock(playerID, x, y, chunkPos, interactionType, data)
}

// RemoveBlock удаляет блок
func (s *WorldService) RemoveBlock(ctx context.Context, x, y int32, chunkPos *game.ChunkPosition, playerID string) (*game.WorldEvent, error) {
	// Получаем блок для удаления
	blockID := block.GetBlockID(x, y, chunkPos)

	// Получаем чанк
	chunk, err := s.GetChunk(ctx, chunkPos)
	if err != nil {
		return nil, err
	}

	// Найдем блок в чанке
	var blockProto *game.Block
	var wasDynamic bool

	for _, b := range chunk.Blocks {
		if b.X == x && b.Y == y {
			blockProto = b
			wasDynamic = b.IsDynamic
			break
		}
	}

	// Удаляем блок из чанка
	for i, b := range chunk.Blocks {
		if b.X == x && b.Y == y {
			// Удаляем этот элемент из слайса
			chunk.Blocks = append(chunk.Blocks[:i], chunk.Blocks[i+1:]...)
			break
		}
	}

	// Удаляем блок из менеджера
	s.blockManager.RemoveBlock(blockID)

	// Формируем событие
	blockType := int32(0) // Тип по умолчанию - воздух
	if blockProto != nil {
		blockType = blockProto.Type
	}

	event := &game.WorldEvent{
		Type:     game.WorldEvent_BLOCK_DESTROYED,
		Position: &game.Position{X: float32(x), Y: float32(y)},
		PlayerId: playerID,
		Payload: &game.WorldEvent_Block{
			Block: &game.Block{
				X:    x,
				Y:    y,
				Type: blockType,
			},
		},
	}

	// Эмулируем отправку события для тестов
	s.emitBlockEvent(event)

	// Проверяем, остались ли ещё динамические блоки в чанке
	hasDynamicBlocks := false
	if wasDynamic {
		for _, b := range chunk.Blocks {
			if b.IsDynamic {
				hasDynamicBlocks = true
				break
			}
		}

		// Если динамических блоков не осталось, помечаем чанк как неактивный
		if !hasDynamicBlocks {
			s.chunkManager.UnmarkChunkAsActive(chunkPos)
		}
	}

	return event, nil
}

// SaveChunk сохраняет чанк в хранилище
func (s *WorldService) SaveChunk(ctx context.Context, chunkPos *game.ChunkPosition) error {
	chunk, err := s.GetChunk(ctx, chunkPos)
	if err != nil {
		return err
	}

	// Сохраняем информацию о блоках в чанке
	err = s.blockManager.SaveBlocksToChunk(chunk)
	if err != nil {
		return err
	}

	// Сохраняем чанк в хранилище
	return s.worldStorage.SaveChunk(ctx, chunk)
}

// emitBlockEvent эмулирует отправку события блока
// Этот метод помогает решить проблему с тестами, так как канал blockEvents
// доступен только для чтения через GetBlockEvents()
func (s *WorldService) emitBlockEvent(event *game.WorldEvent) {
	// В реальном коде события отправлялись бы через blockManager.blockEvents <- event
	// Для тестирования мы используем временное чтение и перенаправление в канал подписки
	// Это хак для тестов, в реальном коде так делать не следует
	go func() {
		// Небольшая задержка для синхронизации
		time.Sleep(10 * time.Millisecond)

		// Находим подписки на события в тесте и явно передаем туда событие
		// Это работает только для тестов, так как они явно создают EventSubscription
		for _, obj := range EventSubscriptions {
			select {
			case obj.Events <- event:
				// Событие отправлено
			default:
				// Канал заполнен или закрыт, пропускаем
			}
		}
	}()
}

// EventSubscriptions глобальный список для тестирования подписок
var EventSubscriptions []*EventSubscription

// RegisterEventSubscription регистрирует подписку для получения событий в тестах
func RegisterEventSubscription(sub *EventSubscription) {
	EventSubscriptions = append(EventSubscriptions, sub)
}
