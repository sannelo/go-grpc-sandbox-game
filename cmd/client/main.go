package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/noisegeneration"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
	"github.com/nsf/termbox-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	serverAddr = flag.String("server", "localhost:50051", "Адрес сервера и порт")
	playerName = flag.String("name", "Player1", "Имя игрока")
	viewRadius = flag.Int("radius", 1, "Радиус отображения мира")
	debugMode  = flag.Bool("debug", false, "Режим отладки (показать подробную информацию)")
)

// ClientState содержит состояние клиента
type ClientState struct {
	playerID       string
	playerName     string
	position       *game.Position
	health         int32
	selectedItem   int32
	chunks         map[string]*game.Chunk
	chunkRequests  map[string]bool // Какие чанки были запрошены
	serverMessages []string        // Последние сообщения от сервера
	mu             sync.RWMutex
	lastUpdate     time.Time
	isConnected    bool
	// Позиции других игроков, ключ – playerID
	otherPlayers  map[string]*game.Position
	client        game.WorldServiceClient
	stream        game.WorldService_GameStreamClient
	lastBiomeData struct {
		height      float64
		moisture    float64
		temperature float64
	}
}

// newClientState создает новое состояние клиента
func newClientState() *ClientState {
	return &ClientState{
		position:      &game.Position{X: 0, Y: 0, Z: 0},
		health:        100,
		chunks:        make(map[string]*game.Chunk),
		chunkRequests: make(map[string]bool),
		serverMessages: []string{
			"Подключение к серверу...",
		},
		lastUpdate:   time.Now(),
		otherPlayers: make(map[string]*game.Position),
	}
}

// Символы для разных типов блоков
var blockSymbols = map[int32]rune{
	chunkmanager.BlockTypeAir:       ' ', // Пустота
	chunkmanager.BlockTypeGrass:     '_', // Трава
	chunkmanager.BlockTypeDirt:      '.', // Земля
	chunkmanager.BlockTypeStone:     '#', // Камень
	chunkmanager.BlockTypeWater:     '~', // Вода
	chunkmanager.BlockTypeSand:      ',', // Песок
	chunkmanager.BlockTypeWood:      '|', // Дерево
	chunkmanager.BlockTypeLeaves:    '@', // Листва
	chunkmanager.BlockTypeSnow:      '*', // Снег
	chunkmanager.BlockTypeTallGrass: '"', // Высокая трава
	chunkmanager.BlockTypeFlower:    'f', // Цветок
}

// Цвета для разных типов блоков
var blockColors = map[int32]termbox.Attribute{
	chunkmanager.BlockTypeAir:       termbox.ColorDefault,
	chunkmanager.BlockTypeGrass:     termbox.ColorGreen,
	chunkmanager.BlockTypeDirt:      termbox.ColorYellow,
	chunkmanager.BlockTypeStone:     termbox.ColorWhite,
	chunkmanager.BlockTypeWater:     termbox.ColorBlue,
	chunkmanager.BlockTypeSand:      termbox.ColorYellow,
	chunkmanager.BlockTypeWood:      termbox.ColorRed,
	chunkmanager.BlockTypeLeaves:    termbox.ColorGreen,
	chunkmanager.BlockTypeSnow:      termbox.ColorWhite,
	chunkmanager.BlockTypeTallGrass: termbox.ColorGreen,
	chunkmanager.BlockTypeFlower:    termbox.ColorMagenta,
}

// Фоновые цвета для блоков
var blockBackgroundColors = map[int32]termbox.Attribute{
	chunkmanager.BlockTypeAir:       termbox.ColorDefault,
	chunkmanager.BlockTypeGrass:     termbox.ColorBlack,    // Черный фон для травы
	chunkmanager.BlockTypeDirt:      termbox.ColorBlack,    // Черный фон для земли
	chunkmanager.BlockTypeStone:     termbox.ColorDarkGray, // Темно-серый для камня
	chunkmanager.BlockTypeWater:     termbox.ColorBlack,    // Черный фон для воды
	chunkmanager.BlockTypeSand:      termbox.ColorBlack,    // Черный фон для песка
	chunkmanager.BlockTypeWood:      termbox.ColorBlack,    // Черный фон для дерева
	chunkmanager.BlockTypeLeaves:    termbox.ColorBlack,    // Черный фон для листвы
	chunkmanager.BlockTypeSnow:      termbox.ColorBlue,     // Синий фон для снега
	chunkmanager.BlockTypeTallGrass: termbox.ColorBlack,    // Черный фон для высокой травы
	chunkmanager.BlockTypeFlower:    termbox.ColorBlack,    // Черный фон для цветка
}

// getChunkKey возвращает строковый ключ для чанка
func getChunkKey(x, y int32) string {
	return fmt.Sprintf("%d:%d", x, y)
}

// getChunkPos возвращает координаты чанка для заданной позиции
func getChunkPos(x, y float32) (int32, int32) {
	chunkX := int32(x) / chunkmanager.ChunkSize
	chunkY := int32(y) / chunkmanager.ChunkSize
	return chunkX, chunkY
}

// getBlockLocal возвращает локальные координаты блока внутри чанка
func getBlockLocal(x, y float32) (int32, int32) {
	blockX := int32(x) % chunkmanager.ChunkSize
	blockY := int32(y) % chunkmanager.ChunkSize
	if blockX < 0 {
		blockX += chunkmanager.ChunkSize
	}
	if blockY < 0 {
		blockY += chunkmanager.ChunkSize
	}
	return blockX, blockY
}

// getBlock возвращает тип блока в указанной позиции
func (cs *ClientState) getBlock(x, y float32) int32 {
	chunkX, chunkY := getChunkPos(x, y)
	blockX, blockY := getBlockLocal(x, y)

	cs.mu.RLock()
	defer cs.mu.RUnlock()

	chunkKey := getChunkKey(chunkX, chunkY)
	chunk, exists := cs.chunks[chunkKey]
	if !exists {
		// Если чанк еще не загружен, запрашиваем его
		if !cs.chunkRequests[chunkKey] {
			// Отмечаем, что чанк запрошен, чтобы не запрашивать его многократно
			cs.chunkRequests[chunkKey] = true

			// Запускаем запрос чанка в отдельной горутине с задержкой
			go func(x, y int32) {
				// Добавляем случайную задержку для предотвращения перегрузки сервера
				time.Sleep(time.Duration(rand.Intn(300)) * time.Millisecond)
				cs.requestChunk(x, y)
			}(chunkX, chunkY)
		}
		return chunkmanager.BlockTypeAir // Возвращаем воздух для незагруженных чанков
	}

	// Ищем блок с заданными координатами
	for _, block := range chunk.Blocks {
		if block.X == blockX && block.Y == blockY {
			return block.Type
		}
	}

	return chunkmanager.BlockTypeAir
}

// addServerMessage добавляет сообщение в список сообщений
func (cs *ClientState) addServerMessage(message string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Добавляем новое сообщение в начало списка
	cs.serverMessages = append([]string{message}, cs.serverMessages...)

	// Ограничиваем количество сообщений
	if len(cs.serverMessages) > 5 {
		cs.serverMessages = cs.serverMessages[:5]
	}
}

// requestChunk запрашивает чанк с сервера
func (cs *ClientState) requestChunk(chunkX, chunkY int32) {
	if cs.client == nil || cs.playerID == "" {
		return
	}

	// Проверяем, был ли уже запрошен этот чанк
	chunkKey := getChunkKey(chunkX, chunkY)
	cs.mu.Lock()
	isRequested := cs.chunkRequests[chunkKey]
	cs.mu.Unlock()

	// Если чанк уже был запрошен, не запрашиваем его повторно
	if !isRequested {
		return
	}

	// Создаем запрос на получение чанка
	chunkRequest := &game.ChunkRequest{
		PlayerPosition: &game.Position{
			X: float32(chunkX * chunkmanager.ChunkSize),
			Y: float32(chunkY * chunkmanager.ChunkSize),
			Z: 0,
		},
		Radius:   1, // Установлен минимальный радиус для улучшения производительности
		PlayerId: cs.playerID,
	}

	// Отправляем запрос на сервер с увеличенным тайм-аутом
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cs.addServerMessage(fmt.Sprintf("Запрос чанка [%d, %d]", chunkX, chunkY))

	stream, err := cs.client.GetChunks(ctx, chunkRequest)
	if err != nil {
		log.Printf("Ошибка при запросе чанка [%d, %d]: %v", chunkX, chunkY, err)
		// Помечаем чанк как не запрошенный, чтобы попробовать позже
		cs.mu.Lock()
		delete(cs.chunkRequests, chunkKey)
		cs.mu.Unlock()
		return
	}

	// Обрабатываем получаемые чанки
	chunksReceived := 0
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Ошибка при получении чанка: %v", err)
			// Помечаем чанк как не запрошенный, чтобы попробовать позже
			cs.mu.Lock()
			delete(cs.chunkRequests, chunkKey)
			cs.mu.Unlock()
			return
		}

		// Сохраняем полученный чанк
		cs.mu.Lock()
		receivedChunkKey := getChunkKey(chunk.Position.X, chunk.Position.Y)
		cs.chunks[receivedChunkKey] = chunk
		cs.mu.Unlock()

		chunksReceived++
	}

	if chunksReceived > 0 {
		cs.addServerMessage(fmt.Sprintf("Получено %d чанков", chunksReceived))
	}
}

// movePlayer перемещает игрока в заданном направлении
func (cs *ClientState) movePlayer(dx, dy float32) {
	if cs.stream == nil {
		return
	}

	// Обновляем позицию игрока
	newX := cs.position.X + dx
	newY := cs.position.Y + dy

	// Отправляем сообщение о движении на сервер
	msg := &game.ClientMessage{
		PlayerId: cs.playerID,
		Payload: &game.ClientMessage_Movement{
			Movement: &game.PlayerMovement{
				NewPosition: &game.Position{
					X: newX,
					Y: newY,
					Z: cs.position.Z,
				},
				Direction: 0, // В данном случае не используется
				IsRunning: false,
			},
		},
	}

	if err := cs.stream.Send(msg); err != nil {
		log.Printf("Ошибка при отправке сообщения о движении: %v", err)
		return
	}

	// Локально обновляем позицию
	cs.mu.Lock()
	cs.position.X = newX
	cs.position.Y = newY
	cs.mu.Unlock()
}

// processInput обрабатывает ввод с клавиатуры
func processInput(cs *ClientState) {
	for {
		switch ev := termbox.PollEvent(); ev.Type {
		case termbox.EventKey:
			switch ev.Key {
			case termbox.KeyEsc, termbox.KeyCtrlC:
				return // Выход из игры
			case termbox.KeyArrowUp:
				cs.movePlayer(0, -1)
			case termbox.KeyArrowDown:
				cs.movePlayer(0, 1)
			case termbox.KeyArrowLeft:
				cs.movePlayer(-1, 0)
			case termbox.KeyArrowRight:
				cs.movePlayer(1, 0)
			case termbox.KeySpace:
				// Создаем сообщение для размещения блока камня
				placeBlockMsg := &game.ClientMessage{
					PlayerId: cs.playerID,
					Payload: &game.ClientMessage_BlockAction{
						BlockAction: &game.BlockAction{
							Action:    game.BlockAction_PLACE,
							Position:  cs.position,
							BlockType: 3, // Тип блока 3 - камень/скала
						},
					},
				}
				// Отправляем сообщение на сервер
				if err := cs.stream.Send(placeBlockMsg); err != nil {
					log.Printf("Ошибка при отправке сообщения для размещения блока: %v", err)
				}
			case termbox.KeyDelete, termbox.KeyBackspace, termbox.KeyBackspace2:
				// Создаем сообщение для удаления блока
				destroyBlockMsg := &game.ClientMessage{
					PlayerId: cs.playerID,
					Payload: &game.ClientMessage_BlockAction{
						BlockAction: &game.BlockAction{
							Action:   game.BlockAction_DESTROY,
							Position: cs.position,
						},
					},
				}
				// Отправляем сообщение на сервер
				if err := cs.stream.Send(destroyBlockMsg); err != nil {
					log.Printf("Ошибка при отправке сообщения для удаления блока: %v", err)
				}
			}

			switch ev.Ch {
			case 'w':
				cs.movePlayer(0, -1)
			case 's':
				cs.movePlayer(0, 1)
			case 'a':
				cs.movePlayer(-1, 0)
			case 'd':
				cs.movePlayer(1, 0)
			case 'q':
				return // Выход из игры
			}
		case termbox.EventError:
			log.Fatalf("Ошибка терминала: %v", ev.Err)
		}
	}
}

// renderWorld отображает мир вокруг игрока
func renderWorld(cs *ClientState) {
	// Очищаем экран
	termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)

	// Получаем размеры терминала
	width, height := termbox.Size()

	// Верхняя информационная панель
	infoY := 0
	playerInfo := fmt.Sprintf("Игрок: %s | HP: %d | X: %.1f | Y: %.1f",
		cs.playerName, cs.health, cs.position.X, cs.position.Y)
	drawText(0, infoY, width, playerInfo, termbox.ColorWhite, termbox.ColorDefault)
	infoY++

	// Если включен режим отладки, показываем дополнительную информацию
	if *debugMode {
		chunkX, chunkY := getChunkPos(cs.position.X, cs.position.Y)
		blockX, blockY := getBlockLocal(cs.position.X, cs.position.Y)
		debugInfo := fmt.Sprintf("Чанк: [%d, %d] | Блок: [%d, %d] | Биом: H=%.2f M=%.2f T=%.2f",
			chunkX, chunkY, blockX, blockY,
			cs.lastBiomeData.height, cs.lastBiomeData.moisture, cs.lastBiomeData.temperature)
		drawText(0, infoY, width, debugInfo, termbox.ColorYellow, termbox.ColorDefault)
		infoY++
	}

	// Граница между инфо-панелью и игровым миром
	for x := 0; x < width; x++ {
		termbox.SetCell(x, infoY, '-', termbox.ColorWhite, termbox.ColorDefault)
	}
	infoY++

	// Вычисляем границы мира для отображения
	startY := infoY
	worldHeight := height - startY - 6 // Оставляем место для сообщений внизу
	worldWidth := width

	// Центрируем игрока на экране
	centerX := int(cs.position.X)
	centerY := int(cs.position.Y)

	// --- Координаты других игроков ---
	cs.mu.RLock()
	remotePosMap := make(map[string]struct{}, len(cs.otherPlayers))
	for _, pos := range cs.otherPlayers {
		if pos == nil {
			continue
		}
		key := fmt.Sprintf("%d:%d", int(pos.X), int(pos.Y))
		remotePosMap[key] = struct{}{}
	}
	cs.mu.RUnlock()

	// Отображаем мир вокруг игрока
	for y := 0; y < worldHeight; y++ {
		for x := 0; x < worldWidth; x++ {
			// Переводим координаты экрана в координаты мира
			worldX := float32(centerX - worldWidth/2 + x)
			worldY := float32(centerY - worldHeight/2 + y)

			// Получаем тип блока
			blockType := cs.getBlock(worldX, worldY)

			// Проверяем, является ли эта позиция позицией игрока
			isPlayer := int(cs.position.X) == int(worldX) && int(cs.position.Y) == int(worldY)

			// Проверяем, находится ли здесь другой игрок
			remoteKey := fmt.Sprintf("%d:%d", int(worldX), int(worldY))
			_, isRemotePlayer := remotePosMap[remoteKey]

			// Отображаем соответствующий символ
			symbol := blockSymbols[blockType]
			fgColor := blockColors[blockType]
			bgColor := blockBackgroundColors[blockType]

			if isPlayer {
				symbol = '@' // Символ игрока
				fgColor = termbox.ColorRed
				bgColor = termbox.ColorDarkGray
			} else if isRemotePlayer {
				symbol = 'P' // Символ другого игрока
				fgColor = termbox.ColorYellow
				bgColor = termbox.ColorDarkGray
			}

			termbox.SetCell(x, y+startY, symbol, fgColor, bgColor)
		}
	}

	// Отображаем сообщения внизу экрана
	msgY := height - 6
	drawText(0, msgY, width, "----- Сообщения -----", termbox.ColorWhite, termbox.ColorDefault)
	msgY++

	for i, msg := range cs.serverMessages {
		if i >= 5 {
			break
		}
		drawText(0, msgY+i, width, msg, termbox.ColorCyan, termbox.ColorDefault)
	}

	// Отображаем инструкции внизу
	helpY := height - 1
	instructions := "Управление: Стрелки/WASD - перемещение, Пробел - поставить камень, Delete/Backspace - удалить блок, Q/Esc - выход"
	drawText(0, helpY, width, instructions, termbox.ColorWhite, termbox.ColorDefault)

	// Обновляем экран
	termbox.Flush()
}

// drawText отображает текст с ограничением по ширине
func drawText(x, y, maxWidth int, text string, fg, bg termbox.Attribute) {
	if len(text) > maxWidth {
		text = text[:maxWidth]
	}

	for i, ch := range text {
		termbox.SetCell(x+i, y, ch, fg, bg)
	}
}

// processServerMessages обрабатывает сообщения от сервера
func processServerMessages(cs *ClientState, stream game.WorldService_GameStreamClient) {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			cs.addServerMessage("Сервер закрыл соединение")
			return
		}
		if err != nil {
			cs.addServerMessage(fmt.Sprintf("Ошибка: %v", err))
			return
		}

		// Обработка полученного сообщения
		switch payload := msg.Payload.(type) {
		case *game.ServerMessage_WorldEvent:
			event := payload.WorldEvent
			if event == nil {
				cs.addServerMessage("Получено пустое событие мира")
				continue
			}

			eventType := ""
			switch event.Type {
			case game.WorldEvent_BLOCK_PLACED:
				eventType = "размещение блока"
				// Обновляем блок в локальном хранилище чанков
				if event.Position != nil {
					handleBlockUpdate(cs, event)
				}
			case game.WorldEvent_BLOCK_DESTROYED:
				eventType = "удаление блока"
				// Обновляем блок в локальном хранилище чанков
				if event.Position != nil {
					handleBlockUpdate(cs, event)
				}
			case game.WorldEvent_ENTITY_SPAWNED:
				eventType = "появление сущности"
			case game.WorldEvent_ENTITY_DESTROYED:
				eventType = "уничтожение сущности"
			case game.WorldEvent_WEATHER_CHANGED:
				eventType = "изменение погоды"
			case game.WorldEvent_TIME_CHANGED:
				eventType = "изменение времени"
			}

			if event.Position != nil {
				cs.addServerMessage(fmt.Sprintf("Событие мира: %s [%.1f, %.1f]",
					eventType, event.Position.X, event.Position.Y))
			} else {
				cs.addServerMessage(fmt.Sprintf("Событие мира: %s", eventType))
			}

		case *game.ServerMessage_PlayerUpdate:
			update := payload.PlayerUpdate
			if update == nil {
				cs.addServerMessage("Получено пустое обновление игрока")
				continue
			}

			// Если это обновление нашего игрока, обновляем позицию
			if update.PlayerId == cs.playerID {
				cs.mu.Lock()
				if update.Position != nil {
					cs.position = update.Position
				}
				cs.health = update.Health
				cs.mu.Unlock()
			} else {
				// Сохраняем/удаляем позицию другого игрока
				cs.mu.Lock()
				if !update.IsConnected {
					delete(cs.otherPlayers, update.PlayerId)
				} else if update.Position != nil {
					cs.otherPlayers[update.PlayerId] = update.Position
				}
				cs.mu.Unlock()

				// Добавляем сообщение о событии
				if update.Position != nil {
					cs.addServerMessage(fmt.Sprintf("Игрок %s: позиция=[%.1f, %.1f]",
						update.PlayerId, update.Position.X, update.Position.Y))
				} else if !update.IsConnected {
					cs.addServerMessage(fmt.Sprintf("Игрок %s отключился", update.PlayerId))
				} else {
					cs.addServerMessage(fmt.Sprintf("Обновление игрока %s", update.PlayerId))
				}
			}

		case *game.ServerMessage_ChunkUpdate:
			update := payload.ChunkUpdate
			if update == nil || update.Position == nil {
				cs.addServerMessage("Получено пустое обновление чанка")
				continue
			}
			cs.addServerMessage(fmt.Sprintf("Обновление чанка [%d, %d]",
				update.Position.X, update.Position.Y))

		case *game.ServerMessage_ChatBroadcast:
			chat := payload.ChatBroadcast
			if chat == nil {
				cs.addServerMessage("Получено пустое сообщение чата")
				continue
			}
			cs.addServerMessage(fmt.Sprintf("%s: %s", chat.PlayerName, chat.Content))

		case *game.ServerMessage_Pong:
			// Пинг не отображаем
		}
	}
}

// handleBlockUpdate обрабатывает обновление блока в мире
func handleBlockUpdate(cs *ClientState, event *game.WorldEvent) {
	if event == nil || event.Position == nil {
		return
	}

	// Получаем координаты чанка
	chunkX := int32(event.Position.X) / chunkmanager.ChunkSize
	chunkY := int32(event.Position.Y) / chunkmanager.ChunkSize

	// Вычисляем локальные координаты блока внутри чанка
	localX := int32(event.Position.X) % chunkmanager.ChunkSize
	if localX < 0 {
		localX += chunkmanager.ChunkSize
	}
	localY := int32(event.Position.Y) % chunkmanager.ChunkSize
	if localY < 0 {
		localY += chunkmanager.ChunkSize
	}

	// Проверяем наличие блока в событии
	block, ok := event.Payload.(*game.WorldEvent_Block)
	if !ok || block == nil || block.Block == nil {
		return
	}

	// Получаем чанк из локального хранилища
	cs.mu.Lock()
	defer cs.mu.Unlock()

	chunkKey := getChunkKey(chunkX, chunkY)
	chunk, exists := cs.chunks[chunkKey]
	if !exists {
		// Если чанк не найден локально, запрашиваем его
		if !cs.chunkRequests[chunkKey] {
			cs.chunkRequests[chunkKey] = true
			go cs.requestChunk(chunkX, chunkY)
		}
		return
	}

	// Ищем блок с такими локальными координатами
	blockFound := false
	for i, existingBlock := range chunk.Blocks {
		if existingBlock != nil && existingBlock.X == localX && existingBlock.Y == localY {
			// Обновляем существующий блок
			chunk.Blocks[i] = block.Block
			blockFound = true
			break
		}
	}

	// Если блок не найден, добавляем новый
	if !blockFound {
		chunk.Blocks = append(chunk.Blocks, block.Block)
	}
}

// simulateBiomeData симулирует данные о биоме для отладки
func simulateBiomeData(cs *ClientState) {
	seed := rand.Int63()
	biomeNoise := noisegeneration.NewBiomeNoise(seed)

	for {
		x, y := cs.position.X, cs.position.Y
		height, moisture, temperature := biomeNoise.GetBiomeData(float64(x), float64(y))

		cs.mu.Lock()
		cs.lastBiomeData.height = height
		cs.lastBiomeData.moisture = moisture
		cs.lastBiomeData.temperature = temperature
		cs.mu.Unlock()

		time.Sleep(200 * time.Millisecond)
	}
}

func main() {
	// Парсим флаги командной строки
	flag.Parse()

	// Инициализируем состояние клиента
	clientState := newClientState()
	clientState.playerName = *playerName

	// Инициализируем терминал
	err := termbox.Init()
	if err != nil {
		log.Fatalf("Не удалось инициализировать терминал: %v", err)
	}
	defer termbox.Close()

	// Устанавливаем соединение с сервером
	conn, err := grpc.Dial(*serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Не удалось подключиться к серверу: %v", err)
	}
	defer conn.Close()

	// Создаем клиент
	client := game.NewWorldServiceClient(conn)
	clientState.client = client

	// Обрабатываем сигналы для корректного завершения
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalChan
		cancel()
		termbox.Close()
		log.Println("Получен сигнал завершения, отключаемся...")
		os.Exit(0)
	}()

	// Присоединяемся к игре
	resp, err := client.JoinGame(ctx, &game.JoinRequest{
		PlayerName: *playerName,
	})
	if err != nil {
		termbox.Close()
		log.Fatalf("Ошибка при подключении к игре: %v", err)
	}

	if !resp.Success {
		termbox.Close()
		log.Fatalf("Не удалось подключиться к игре: %s", resp.ErrorMessage)
	}

	// Сохраняем ID игрока и начальную позицию
	clientState.playerID = resp.PlayerId
	clientState.position = resp.SpawnPosition
	clientState.addServerMessage(fmt.Sprintf("Успешное подключение! ID: %s", resp.PlayerId))

	// Устанавливаем двунаправленный поток для обмена сообщениями
	stream, err := client.GameStream(ctx)
	if err != nil {
		termbox.Close()
		log.Fatalf("Ошибка при создании потока: %v", err)
	}
	clientState.stream = stream

	// Отправляем первое сообщение для инициализации соединения
	initMsg := &game.ClientMessage{
		PlayerId: clientState.playerID,
		Payload: &game.ClientMessage_Ping{
			Ping: &game.Ping{
				Timestamp: time.Now().UnixNano(),
			},
		},
	}
	if err := stream.Send(initMsg); err != nil {
		termbox.Close()
		log.Fatalf("Ошибка при отправке инициализирующего сообщения: %v", err)
	}

	// Запускаем обработку входящих сообщений от сервера
	go processServerMessages(clientState, stream)

	// Запускаем симуляцию данных о биоме (только для отладки)
	go simulateBiomeData(clientState)

	// Запускаем цикл обновления экрана
	go func() {
		for {
			renderWorld(clientState)
			time.Sleep(100 * time.Millisecond)
		}
	}()

	// Запускаем обработку ввода
	processInput(clientState)

	// Завершаем работу
	termbox.Close()
	log.Println("Клиент завершает работу")
}
