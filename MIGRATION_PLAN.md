# План миграции на систему Smart Blocks

## Выполненные изменения

1. **Создан пакет worldinterfaces**
   - Определены интерфейсы для взаимодействия между ChunkManager и BlockManager
   - Устранена циклическая зависимость между пакетами

2. **Обновлен ChunkManager**
   - Добавлена поддержка BlockManager через интерфейс
   - Обновлены методы генерации чанков для создания умных блоков
   - Добавлены методы для взаимодействия с блоками
   - Реализована поддержка активных чанков для динамических блоков

3. **Создан пакет world**
   - Реализована фасадная структура World для инициализации и связывания компонентов
   - Добавлены методы для запуска и остановки мира с умными блоками

## Дальнейшие шаги миграции

### 1. Обновление точек входа сервера
```go
// main.go или server.go
import (
    "github.com/annelo/go-grpc-server/internal/world"
    "github.com/annelo/go-grpc-server/internal/storage"
)

func main() {
    // Инициализация хранилища
    storage := storage.NewBinaryStorage("./world_data")
    
    // Создаем экземпляр мира с умными блоками
    gameWorld := world.NewWorld(rand.New(rand.NewSource(time.Now().UnixNano())), storage)
    
    // Запускаем все необходимые компоненты мира
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    gameWorld.Start(ctx)
    
    // Используем gameWorld.ChunkManager и gameWorld.BlockManager
    // вместо прямого создания ChunkManager
    server := NewGameServer(gameWorld.ChunkManager, gameWorld.BlockManager)
    
    // ...
    // Остальная логика запуска сервера
    // ...
    
    // Корректное завершение при выключении
    defer gameWorld.Stop(context.Background())
}
```

### 2. Обновление обработчиков событий
```go
// Где-то в обработчике запросов
func (s *gameServer) HandleBlockInteraction(ctx context.Context, req *game.BlockInteractionRequest) (*game.BlockInteractionResponse, error) {
    playerID := req.PlayerId
    chunkPos := req.ChunkPosition
    blockX := req.BlockX
    blockY := req.BlockY
    interactionType := req.InteractionType
    data := req.InteractionData
    
    // Используем метод InteractWithBlock из ChunkManager
    event, err := s.chunkManager.InteractWithBlock(playerID, blockX, blockY, chunkPos, interactionType, data)
    if err != nil {
        return nil, err
    }
    
    // Обработка результата и отправка ответа клиенту
    return &game.BlockInteractionResponse{
        Success: true,
        Message: event.Message,
        Block: event.GetBlock(),
    }, nil
}
```

### 3. Добавление обработки событий блоков в игровой цикл

```go
func startEventProcessing(world *world.World) {
    for event := range world.GetEvents() {
        // Отправляем события клиентам
        broadcastEvent(event)
    }
}
```

### 4. Добавление новых типов блоков

1. Создать новый файл с определением блока, например `internal/block/redstone_blocks.go`:
   ```go
   package block
   
   import (
       "time"
       "github.com/annelo/go-grpc-server/pkg/protocol/game"
   )
   
   // Константы для редстоун-блоков
   const (
       BlockTypeRedstoneWire = 40
       BlockTypeRedstoneSource = 41
       // ...
   )
   
   // RedstoneBlock представляет блок, передающий сигнал редстоуна
   type RedstoneBlock struct {
       *BaseBlock
       power int // Сила сигнала (0-15)
       // ...
   }
   
   // Реализация методов для RedstoneBlock
   // ...
   
   // Регистрация блоков в менеджере
   func RegisterRedstoneBlocks(manager *BlockManager) {
       manager.RegisterBlockFactory(BlockTypeRedstoneWire, NewRedstoneWireBlock)
       manager.RegisterBlockFactory(BlockTypeRedstoneSource, NewRedstoneSourceBlock)
       // ...
   }
   ```

2. Обновить регистрацию в конструкторе World:
   ```go
   func NewWorld(rnd *rand.Rand, storage storage.WorldStorage) *World {
       // ...
       
       // Регистрируем все типы блоков
       block.RegisterDynamicBlocks(blockManager)
       block.RegisterInteractiveBlocks(blockManager)
       block.RegisterRedstoneBlocks(blockManager) // Новая группа блоков
       
       // ...
   }
   ```

### 5. Оптимизация системы планирования обновлений

1. Улучшение очереди обновлений в BlockManager для поддержки большого количества блоков
2. Реализация пространственного разделения для оптимизации обновлений
3. Введение уровней приоритета для обновления чанков

### 6. Тестирование

1. Написать юнит-тесты для новых компонентов
2. Провести интеграционные тесты взаимодействия между ChunkManager и BlockManager
3. Выполнить нагрузочное тестирование с большим количеством динамических блоков

## Потенциальные проблемы и решения

1. **Производительность**: Большое количество динамических блоков может создать высокую нагрузку
   - Решение: Оптимизировать систему планирования, группировать обновления, использовать многоуровневую систему приоритетов

2. **Сетевой трафик**: Частые обновления состояния блоков могут создать большой трафик
   - Решение: Реализовать батчинг обновлений, приоритизировать обновления по важности, использовать дельта-сжатие

3. **Совместимость с существующими клиентами**: Старые клиенты могут не поддерживать Smart Blocks
   - Решение: Обеспечить обратную совместимость, добавить проверку версии клиента и предоставлять базовое представление для старых клиентов

4. **Обратная совместимость с существующими мирами**: Старые миры не содержат умных блоков
   - Решение: Реализовать автоматическую конвертацию при загрузке старых чанков

## Заключение

Реализованные изменения позволяют перейти на архитектуру Smart Blocks с минимальными изменениями в остальной кодовой базе. Система обеспечивает хорошую масштабируемость и расширяемость, позволяя легко добавлять новые типы блоков с различным поведением.

Использование слабосвязанных компонентов через интерфейсы обеспечивает гибкость и облегчает тестирование. Разделение ответственности между ChunkManager и BlockManager соответствует принципам SOLID и обеспечивает чистую архитектуру системы.

Поэтапный переход с поддержкой обратной совместимости обеспечит плавную миграцию без потери данных и нарушения работы существующей функциональности. 