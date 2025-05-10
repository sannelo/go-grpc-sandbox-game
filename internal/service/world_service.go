package service

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"expvar"

	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/playermanager"
	"github.com/annelo/go-grpc-server/internal/storage"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/annelo/go-grpc-server/internal/gameloop"
)

// WorldService представляет собой реализацию gRPC сервиса игрового мира
type WorldService struct {
	game.UnimplementedWorldServiceServer
	playerManager *playermanager.PlayerManager
	chunkManager  *chunkmanager.ChunkManager
	worldStorage  storage.WorldStorage

	// Мьютекс для синхронизации доступа к внутренним структурам
	mu sync.RWMutex

	// Карта активных клиентских соединений
	clientStreams map[string]*clientConn

	// Throttle карты: последний момент, когда позиция игрока была разослана
	lastPosBroadcast map[string]time.Time
	throttleMu       sync.Mutex

	// игровая петля
	loop *gameloop.Loop
}

type clientConn struct {
	stream    game.WorldService_GameStreamServer
	sendQueue chan *game.ServerMessage
}

const (
	// Минимальный интервал рассылки позиций одного игрока (throttle)
	playerBroadcastMinInterval = 200 * time.Millisecond

	// Радиус видимости в чанках: обновление отправляется только игрокам, чьи
	// позиции ближе этого расстояния до обновляемого игрока.
	visibilityChunkRadius = 8

	sendQueueSize = 1024
)

// NewWorldService создает новый экземпляр сервиса игрового мира
func NewWorldService() *WorldService {
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	return &WorldService{
		playerManager:    playermanager.NewPlayerManager(),
		chunkManager:     chunkmanager.NewChunkManager(rnd),
		clientStreams:    make(map[string]*clientConn),
		lastPosBroadcast: make(map[string]time.Time),
	}
}

// NewWorldServiceWithStorage создает новый экземпляр сервиса игрового мира с хранилищем
func NewWorldServiceWithStorage(worldStorage storage.WorldStorage) *WorldService {
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Получаем информацию о мире из хранилища
	worldInfo, err := worldStorage.LoadWorld(context.Background())
	if err == nil {
		// Если информация о мире существует, используем её сид
		rnd = rand.New(rand.NewSource(worldInfo.Seed))
	}

	ws := &WorldService{
		playerManager:    playermanager.NewPlayerManager(),
		chunkManager:     chunkmanager.NewChunkManagerWithStorage(rnd, worldStorage),
		clientStreams:    make(map[string]*clientConn),
		worldStorage:     worldStorage,
		lastPosBroadcast: make(map[string]time.Time),
	}
	return ws
}

// Start инициализирует и запускает необходимые сервисные задачи
func (s *WorldService) Start(ctx context.Context) {
	// Подготавливаем хранилище и загружаем данные
	if s.chunkManager.HasStorage() {
		log.Println("Загружаем чанки из хранилища...")
		// Запускаем периодическое сохранение
		s.chunkManager.StartPeriodicalSaving(ctx, 30*time.Second)
		// Запускаем очистку кэша и выгрузку неиспользуемых чанков
		s.chunkManager.StartCacheCleanupRoutine(ctx, 2*time.Minute)
		err := s.chunkManager.LoadChunksFromStorage(ctx)
		if err != nil {
			log.Printf("Ошибка при загрузке чанков из хранилища: %v", err)
		} else {
			log.Printf("Чанки успешно загружены из хранилища")
		}
	}

	// Запускаем периодический мониторинг кеша
	go s.monitorCacheUsage(ctx)

	// --- GameLoop ---
	deps := gameloop.Dependencies{
		Players:        s.playerManager,
		Chunks:         s.chunkManager,
		EmitWorldEvent: s.broadcastWorldEvent,
	}

	s.loop = gameloop.NewLoop(50*time.Millisecond, deps,
		gameloop.NewTimeSystem(),
		gameloop.NewWeatherSystem(time.Now().UnixNano()),
	)
	go s.loop.Run(ctx)
}

// monitorCacheUsage периодически логирует статистику использования кеша
func (s *WorldService) monitorCacheUsage(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			stats := s.chunkManager.GetCacheStats()
			log.Printf("Статистика кеша: %+v", stats)
		case <-ctx.Done():
			return
		}
	}
}

// RegisterServer регистрирует сервис в gRPC сервере
func (s *WorldService) RegisterServer(grpcServer *grpc.Server) {
	game.RegisterWorldServiceServer(grpcServer, s)
}

// JoinGame обрабатывает запрос на присоединение игрока к игре
func (s *WorldService) JoinGame(ctx context.Context, req *game.JoinRequest) (*game.JoinResponse, error) {
	// Генерируем уникальный ID для игрока
	playerID := uuid.New().String()

	// Создаем начальную позицию для игрока
	spawnPos := &game.Position{
		X: 1000.0,
		Y: 1000.0,
		Z: 0,
	}

	// Добавляем игрока в менеджер игроков
	err := s.playerManager.AddPlayer(playerID, req.PlayerName, spawnPos)
	if err != nil {
		return &game.JoinResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("Не удалось добавить игрока: %v", err),
		}, nil
	}

	log.Printf("Игрок %s (%s) присоединился к игре", req.PlayerName, playerID)
	expvar.Get("players_connected").(*expvar.Int).Add(1)

	// Если есть хранилище — пробуем загрузить сохранённое состояние
	if s.worldStorage != nil {
		if ps, err := s.worldStorage.LoadPlayerState(ctx, playerID); err == nil {
			// Перезаписываем позицию/здоровье
			player, err := s.playerManager.GetPlayer(playerID)
			if err == nil {
				player.Position = ps.Position
				player.Health = ps.Health
			}
		}
	}

	// Возвращаем ответ с ID игрока и начальной позицией
	return &game.JoinResponse{
		PlayerId:      playerID,
		SpawnPosition: spawnPos,
		Success:       true,
	}, nil
}

// GameStream устанавливает двунаправленный поток для обмена игровыми событиями
func (s *WorldService) GameStream(stream game.WorldService_GameStreamServer) error {
	// Ожидаем первое сообщение чтобы определить ID игрока
	clientMsg, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.Internal, "Ошибка при получении первого сообщения: %v", err)
	}

	playerID := clientMsg.PlayerId
	player, err := s.playerManager.GetPlayer(playerID)
	if err != nil {
		return status.Errorf(codes.NotFound, "Игрок не найден: %v", err)
	}

	// Регистрируем поток для этого игрока
	s.mu.Lock()
	conn := &clientConn{stream: stream, sendQueue: make(chan *game.ServerMessage, sendQueueSize)}
	s.clientStreams[playerID] = conn
	s.mu.Unlock()

	// Оповещаем других игроков о подключении нового игрока
	s.broadcastPlayerUpdate(playerID, player.Position, true)

	// sender goroutine
	go func() {
		for msg := range conn.sendQueue {
			if err := conn.stream.Send(msg); err != nil {
				log.Printf("Ошибка отправки клиенту %s: %v", playerID, err)
				return
			}
		}
	}()

	// Обрабатываем входящие сообщения от клиента
	for {
		clientMsg, err := stream.Recv()
		if err != nil {
			log.Printf("Соединение с игроком %s потеряно: %v", playerID, err)
			break
		}

		// Обрабатываем сообщение в зависимости от его типа
		s.handleClientMessage(clientMsg, stream)
	}

	// Закрываем очередь и удаляем
	s.mu.Lock()
	if conn, ok := s.clientStreams[playerID]; ok {
		close(conn.sendQueue)
		delete(s.clientStreams, playerID)
	}
	s.mu.Unlock()

	// Оповещаем других игроков об отключении
	s.broadcastPlayerUpdate(playerID, nil, false)
	expvar.Get("players_connected").(*expvar.Int).Add(-1)

	// Сохраняем состояние игрока
	if s.worldStorage != nil {
		s.worldStorage.SavePlayerState(context.Background(), &storage.PlayerState{
			ID:       playerID,
			Name:     player.Name,
			Position: player.Position,
			Health:   player.Health,
			LastSeen: time.Now().Unix(),
		})
	}

	log.Printf("Игрок %s отключился", playerID)
	return nil
}

// GetChunks обрабатывает запрос на получение чанков мира вокруг игрока
func (s *WorldService) GetChunks(req *game.ChunkRequest, stream game.WorldService_GetChunksServer) error {
	// Проверяем существование игрока
	player, err := s.playerManager.GetPlayer(req.PlayerId)
	if err != nil {
		return status.Errorf(codes.NotFound, "Игрок не найден: %v", err)
	}

	log.Printf("Получен запрос на чанки от игрока %s (%s), позиция [%.1f, %.1f], радиус %d",
		player.Name, req.PlayerId, req.PlayerPosition.X, req.PlayerPosition.Y, req.Radius)

	// Получаем позицию игрока и радиус запрашиваемых чанков
	playerPos := req.PlayerPosition

	// Ограничиваем радиус для улучшения производительности
	radius := req.Radius
	if radius > 2 {
		radius = 2 // Максимальный радиус - 2 чанка
		log.Printf("Радиус запроса сокращен до %d для игрока %s", radius, req.PlayerId)
	}

	// Вычисляем координаты чанка, в котором находится игрок
	playerChunkX := int32(playerPos.X) / chunkmanager.ChunkSize
	playerChunkY := int32(playerPos.Y) / chunkmanager.ChunkSize

	log.Printf("Игрок находится в чанке [%d, %d]", playerChunkX, playerChunkY)

	// Получаем и отправляем чанки в указанном радиусе
	chunksTotal := 0
	chunksSent := 0

	for y := playerChunkY - radius; y <= playerChunkY+radius; y++ {
		for x := playerChunkX - radius; x <= playerChunkX+radius; x++ {
			chunksTotal++
			chunkPos := &game.ChunkPosition{X: x, Y: y}

			// Получаем чанк (или генерируем, если его нет)
			chunk, err := s.chunkManager.GetOrGenerateChunk(chunkPos)
			if err != nil {
				log.Printf("Ошибка при получении чанка [%d, %d]: %v", x, y, err)
				continue
			}

			// Отправляем чанк клиенту
			if err := stream.Send(chunk); err != nil {
				log.Printf("Ошибка при отправке чанка [%d, %d]: %v", x, y, err)
				return status.Errorf(codes.Internal, "Ошибка при отправке чанка: %v", err)
			}
			chunksSent++
		}
	}

	log.Printf("Отправлено %d/%d чанков игроку %s", chunksSent, chunksTotal, req.PlayerId)
	return nil
}

// GenerateChunks генерирует указанные чанки и возвращает результат
func (s *WorldService) GenerateChunks(ctx context.Context, req *game.GenerateRequest) (*game.GenerateResponse, error) {
	// Проверяем существование игрока
	_, err := s.playerManager.GetPlayer(req.PlayerId)
	if err != nil {
		return &game.GenerateResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("Игрок не найден: %v", err),
		}, nil
	}

	// Генерируем запрошенные чанки
	for _, pos := range req.Positions {
		_, err := s.chunkManager.GetOrGenerateChunk(pos)
		if err != nil {
			return &game.GenerateResponse{
				Success:      false,
				ErrorMessage: fmt.Sprintf("Ошибка при генерации чанка [%d, %d]: %v", pos.X, pos.Y, err),
			}, nil
		}
	}

	return &game.GenerateResponse{
		Success: true,
	}, nil
}

// handleClientMessage обрабатывает сообщение от клиента
func (s *WorldService) handleClientMessage(msg *game.ClientMessage, stream game.WorldService_GameStreamServer) {
	playerID := msg.PlayerId

	switch payload := msg.Payload.(type) {
	case *game.ClientMessage_Movement:
		// Обработка движения игрока
		movement := payload.Movement
		s.handlePlayerMovement(playerID, movement)

	case *game.ClientMessage_BlockAction:
		// Обработка действия с блоком
		blockAction := payload.BlockAction
		s.handleBlockAction(playerID, blockAction)

	case *game.ClientMessage_Chat:
		// Обработка сообщения чата
		chatMsg := payload.Chat
		s.handleChatMessage(playerID, chatMsg)

	case *game.ClientMessage_Ping:
		// Обработка пинга (просто отправляем обратно pong)
		pingMsg := payload.Ping
		s.handlePing(playerID, pingMsg)
	}
}

// handlePlayerMovement обрабатывает сообщение о движении игрока
func (s *WorldService) handlePlayerMovement(playerID string, movement *game.PlayerMovement) {
	// Обновляем позицию игрока
	err := s.playerManager.UpdatePlayerPosition(playerID, movement.NewPosition)
	if err != nil {
		log.Printf("Ошибка при обновлении позиции игрока %s: %v", playerID, err)
		return
	}

	// Отправляем обновление всем игрокам
	s.broadcastPlayerUpdate(playerID, movement.NewPosition, true)
}

// canPlaceBlock проверяет, может ли игрок разместить блок в указанной позиции
func (s *WorldService) canPlaceBlock(playerID string, position *game.Position, blockType int32) bool {
	// В тестовой реализации всегда разрешаем размещение блоков
	// TODO: В будущем здесь должны быть проверки:
	// 1. Имеет ли игрок право размещать блоки
	// 2. Находится ли позиция в допустимом диапазоне от игрока
	// 3. Можно ли разместить блок в этой позиции (нет конфликтов и т.д.)
	// 4. Есть ли у игрока необходимые ресурсы для размещения блока

	return true
}

// handleBlockAction обрабатывает действие игрока с блоком
func (s *WorldService) handleBlockAction(playerID string, action *game.BlockAction) {
	// Получаем координаты чанка для позиции блока
	blockPos := action.Position
	log.Printf("Обработка действия с блоком от игрока %s: действие=%v, позиция=[%.1f, %.1f], тип блока=%d",
		playerID, action.Action, blockPos.X, blockPos.Y, action.BlockType)

	chunkX := int32(blockPos.X) / chunkmanager.ChunkSize
	chunkY := int32(blockPos.Y) / chunkmanager.ChunkSize
	chunkPos := &game.ChunkPosition{X: chunkX, Y: chunkY}

	// Получаем чанк
	chunk, err := s.chunkManager.GetOrGenerateChunk(chunkPos)
	if err != nil {
		log.Printf("Ошибка при получении чанка [%d, %d] для действия с блоком: %v",
			chunkX, chunkY, err)
		return
	}

	log.Printf("Чанк [%d, %d] успешно получен, содержит %d блоков",
		chunkPos.X, chunkPos.Y, len(chunk.Blocks))

	// Вычисляем локальные координаты блока внутри чанка
	localX := int32(blockPos.X) % chunkmanager.ChunkSize
	if localX < 0 {
		localX += chunkmanager.ChunkSize
	}
	localY := int32(blockPos.Y) % chunkmanager.ChunkSize
	if localY < 0 {
		localY += chunkmanager.ChunkSize
	}

	log.Printf("Локальные координаты блока в чанке: [%d, %d]", localX, localY)

	// Выполняем действие в зависимости от его типа
	switch action.Action {
	case game.BlockAction_PLACE:
		// Проверяем, может ли игрок разместить блок
		if !s.canPlaceBlock(playerID, blockPos, action.BlockType) {
			log.Printf("Игроку %s запрещено размещать блок типа %d в позиции [%f, %f]",
				playerID, action.BlockType, blockPos.X, blockPos.Y)
			return
		}

		// Создаем новый блок
		newBlock := &game.Block{
			X:    localX,
			Y:    localY,
			Type: action.BlockType,
		}

		// Обновляем чанк
		err = s.chunkManager.SetBlock(chunkPos, newBlock)
		if err != nil {
			log.Printf("Ошибка при размещении блока: %v", err)
			return
		}

		log.Printf("Игрок %s успешно разместил блок типа %d в позиции [%f, %f], локальные координаты [%d, %d]",
			playerID, action.BlockType, blockPos.X, blockPos.Y, localX, localY)

		// Отправляем событие всем игрокам
		worldEvent := &game.WorldEvent{
			Type:     game.WorldEvent_BLOCK_PLACED,
			Position: blockPos,
			PlayerId: playerID,
			Payload: &game.WorldEvent_Block{
				Block: newBlock,
			},
		}

		// Сразу отправляем инициатору гарантированно
		if conn, ok := s.clientStreams[playerID]; ok {
			conn.send(&game.ServerMessage{Payload: &game.ServerMessage_WorldEvent{WorldEvent: worldEvent}}, true)
		}

		s.broadcastWorldEvent(worldEvent)

	case game.BlockAction_DESTROY:
		// Удаляем блок (устанавливаем тип 0 - воздух)
		newBlock := &game.Block{
			X:    localX,
			Y:    localY,
			Type: 0, // Воздух или пустота
		}

		// Обновляем чанк
		err = s.chunkManager.SetBlock(chunkPos, newBlock)
		if err != nil {
			log.Printf("Ошибка при удалении блока: %v", err)
			return
		}

		log.Printf("Игрок %s успешно уничтожил блок в позиции [%f, %f], локальные координаты [%d, %d]",
			playerID, blockPos.X, blockPos.Y, localX, localY)

		// Отправляем событие всем игрокам
		worldEvent := &game.WorldEvent{
			Type:     game.WorldEvent_BLOCK_DESTROYED,
			Position: blockPos,
			PlayerId: playerID,
			Payload: &game.WorldEvent_Block{
				Block: newBlock,
			},
		}

		if conn, ok := s.clientStreams[playerID]; ok {
			conn.send(&game.ServerMessage{Payload: &game.ServerMessage_WorldEvent{WorldEvent: worldEvent}}, true)
		}

		s.broadcastWorldEvent(worldEvent)

	case game.BlockAction_INTERACT:
		// Взаимодействие с блоком (пока не реализовано)
		log.Printf("Взаимодействие с блоком не реализовано")
	}

	// Временно отключаем сохранение чанков
	/* Закомментировано для отладки
	// Сохраняем изменённый чанк, если включено хранилище
	if s.chunkManager.HasStorage() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.chunkManager.SaveChunk(ctx, chunkPos); err != nil {
				log.Printf("Ошибка при сохранении чанка [%d, %d]: %v", chunkPos.X, chunkPos.Y, err)
			} else {
				log.Printf("Чанк [%d, %d] успешно сохранен в хранилище", chunkPos.X, chunkPos.Y)
			}
		}()
	}
	*/
}

// handleChatMessage обрабатывает сообщение чата
func (s *WorldService) handleChatMessage(playerID string, chatMsg *game.ChatMessage) {
	// Получаем информацию об игроке
	player, err := s.playerManager.GetPlayer(playerID)
	if err != nil {
		log.Printf("Игрок не найден для сообщения чата: %v", err)
		return
	}

	// Создаем сообщение для broadcast
	broadcast := &game.ChatBroadcast{
		PlayerId:       playerID,
		PlayerName:     player.Name,
		Content:        chatMsg.Content,
		IsGlobal:       chatMsg.IsGlobal,
		TargetPlayerId: chatMsg.TargetPlayerId,
	}

	// Отправляем сообщение всем или конкретному игроку
	if chatMsg.IsGlobal || chatMsg.TargetPlayerId == "" {
		// Всем игрокам
		s.broadcastToAll(&game.ServerMessage{
			Payload: &game.ServerMessage_ChatBroadcast{
				ChatBroadcast: broadcast,
			},
		})
	} else {
		// Только отправителю и получателю
		serverMsg := &game.ServerMessage{
			Payload: &game.ServerMessage_ChatBroadcast{
				ChatBroadcast: broadcast,
			},
		}

		// Отправляем получателю
		s.mu.RLock()
		if targetStream, ok := s.clientStreams[chatMsg.TargetPlayerId]; ok {
			targetStream.sendQueue <- serverMsg
		}
		s.mu.RUnlock()

		// Отправляем отправителю (если он не получатель)
		if playerID != chatMsg.TargetPlayerId {
			s.mu.RLock()
			if sourceStream, ok := s.clientStreams[playerID]; ok {
				sourceStream.sendQueue <- serverMsg
			}
			s.mu.RUnlock()
		}
	}
}

// handlePing обрабатывает пинг от клиента
func (s *WorldService) handlePing(playerID string, ping *game.Ping) {
	// Создаем ответный pong
	pong := &game.Pong{
		ClientTimestamp: ping.Timestamp,
		ServerTimestamp: time.Now().UnixNano(),
	}

	// Отправляем ответ только отправителю пинга
	s.mu.RLock()
	if stream, ok := s.clientStreams[playerID]; ok {
		stream.sendQueue <- &game.ServerMessage{
			Payload: &game.ServerMessage_Pong{
				Pong: pong,
			},
		}
	}
	s.mu.RUnlock()
}

// broadcastPlayerUpdate отправляет обновление позиции игрока всем игрокам
func (s *WorldService) broadcastPlayerUpdate(playerID string, position *game.Position, isConnected bool) {
	// Throttle: проверяем, достаточно ли времени прошло с предыдущей рассылки
	s.throttleMu.Lock()
	last, ok := s.lastPosBroadcast[playerID]
	if ok && time.Since(last) < playerBroadcastMinInterval {
		s.throttleMu.Unlock()
		return
	}
	s.lastPosBroadcast[playerID] = time.Now()
	s.throttleMu.Unlock()

	// Получаем информацию об игроке
	player, err := s.playerManager.GetPlayer(playerID)
	if err != nil {
		log.Printf("Не удалось найти игрока для обновления: %v", err)
		return
	}

	// Формируем обновление
	update := &game.PlayerUpdate{
		PlayerId:    playerID,
		Position:    position,
		Health:      player.Health,
		IsConnected: isConnected,
	}

	// Если позиции нет (дисконнект) — рассылаем всем
	if position == nil {
		s.broadcastToAll(&game.ServerMessage{Payload: &game.ServerMessage_PlayerUpdate{PlayerUpdate: update}})
		return
	}

	// Рассылаем только игрокам в пределах видимости
	s.mu.RLock()
	for targetID, conn := range s.clientStreams {
		// Получаем позицию принимающего игрока
		tgt, err := s.playerManager.GetPlayer(targetID)
		if err != nil || tgt.Position == nil {
			continue
		}

		if !isWithinRadius(position, tgt.Position) {
			continue // за пределами радиуса
		}

		// неблокирующая отправка
		conn.send(&game.ServerMessage{Payload: &game.ServerMessage_PlayerUpdate{PlayerUpdate: update}}, false)
	}
	s.mu.RUnlock()
}

// isWithinRadius проверяет, находятся ли позиции в пределах visibilityChunkRadius чанков.
func isWithinRadius(p1, p2 *game.Position) bool {
	if p1 == nil || p2 == nil {
		return true // если нет позиции, не фильтруем
	}
	dx := int32(p1.X - p2.X)
	dy := int32(p1.Y - p2.Y)

	// Переводим из блоков в чанки
	cx := dx / chunkmanager.ChunkSize
	cy := dy / chunkmanager.ChunkSize

	if cx < 0 {
		cx = -cx
	}
	if cy < 0 {
		cy = -cy
	}

	if cx > visibilityChunkRadius || cy > visibilityChunkRadius {
		return false
	}
	return true
}

// broadcastWorldEvent отправляет событие мира всем игрокам
func (s *WorldService) broadcastWorldEvent(event *game.WorldEvent) {
	// Отправляем сообщение всем игрокам
	s.broadcastToAll(&game.ServerMessage{
		Payload: &game.ServerMessage_WorldEvent{
			WorldEvent: event,
		},
	})
}

// broadcastToAll отправляет сообщение всем подключенным игрокам
func (s *WorldService) broadcastToAll(message *game.ServerMessage) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, conn := range s.clientStreams {
		conn.send(message, false)
	}
}

// Stop сохраняет все данные и закрывает стораджи
func (s *WorldService) Stop() {
	// Отключаем всех клиентов
	s.DisconnectAllClients()

	// Сохраняем все игроки
	if s.worldStorage != nil {
		players := s.playerManager.GetAllPlayers()
		for _, p := range players {
			_ = s.worldStorage.SavePlayerState(context.Background(), &storage.PlayerState{
				ID:       p.ID,
				Name:     p.Name,
				Position: p.Position,
				Health:   p.Health,
				LastSeen: time.Now().Unix(),
			})
		}
		// Сохраняем все чанки
		s.chunkManager.SaveChunksToStorage(context.Background())
		s.worldStorage.Close()
	}
}

// DisconnectAllClients отключает всех подключенных клиентов, отправляя им сообщение о завершении работы сервера
func (s *WorldService) DisconnectAllClients() {
	log.Println("Отключение всех клиентов...")

	// Отправляем всем клиентам сообщение о завершении работы сервера
	disconnectMessage := &game.ServerMessage{
		Payload: &game.ServerMessage_WorldEvent{
			WorldEvent: &game.WorldEvent{
				Type:    game.WorldEvent_SERVER_SHUTDOWN,
				Message: "Сервер завершает работу",
			},
		},
	}

	// Блокирующая отправка сообщений всем клиентам
	s.mu.Lock()
	clientCount := len(s.clientStreams)
	for _, conn := range s.clientStreams {
		conn.send(disconnectMessage, true) // блокирующая отправка, важно доставить!

		// Закрываем очередь
		close(conn.sendQueue)
	}
	// Очищаем мапу соединений
	s.clientStreams = make(map[string]*clientConn)
	s.mu.Unlock()

	log.Printf("Отключено %d клиентов", clientCount)
}

// send помещает сообщение в очередь.
// Если block==true – будет ожидать, пока появится место.
// Если block==false – при переполнении сообщение дропается.
func (c *clientConn) send(msg *game.ServerMessage, block bool) {
	if block {
		c.sendQueue <- msg
		return
	}
	select {
	case c.sendQueue <- msg:
	default:
		// очередь заполнена – пропускаем
	}
}

func init() {
	// Инициализируем expvar-счётчики, если приложение запускается без server/main (например, в тестах)
	ensureCounter := func(name string) {
		if expvar.Get(name) == nil {
			expvar.NewInt(name)
		}
	}
	ensureCounter("players_connected")
	ensureCounter("chunks_saved")
	ensureCounter("region_compactions")
}
