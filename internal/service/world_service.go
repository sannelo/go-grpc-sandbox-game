package service

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"

	"go.uber.org/zap"

	"expvar"

	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/playermanager"
	"github.com/annelo/go-grpc-server/internal/storage"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"

	"github.com/annelo/go-grpc-server/internal/block"
	"github.com/annelo/go-grpc-server/internal/gameloop"
	"github.com/annelo/go-grpc-server/internal/plugin"
)

// WorldService представляет собой реализацию gRPC сервиса игрового мира
type WorldService struct {
	game.UnimplementedWorldServiceServer
	// logger for structured logging
	logger        *zap.SugaredLogger
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

	// Менеджер блоков
	blockManager *block.BlockManager

	// Реестр плагинов с фабриками блоков и системами
	registry plugin.PluginRegistry
}

const (
	// sendQueueSize is the maximum number of messages in send queues per client.
	sendQueueSize = 1024
)

// Добавляю adapter для привязки block.BlockManager к worldinterfaces.BlockManagerInterface
type blockManagerAdapter struct {
	bm *block.BlockManager
}

func (a *blockManagerAdapter) CreateBlock(x, y int32, chunkPos *game.ChunkPosition, blockType int32) (interface{}, error) {
	return a.bm.CreateBlock(x, y, chunkPos, blockType)
}
func (a *blockManagerAdapter) LoadBlocksFromChunk(chunk *game.Chunk) error {
	return a.bm.LoadBlocksFromChunk(chunk)
}
func (a *blockManagerAdapter) SaveBlocksToChunk(chunk *game.Chunk) error {
	return a.bm.SaveBlocksToChunk(chunk)
}
func (a *blockManagerAdapter) InteractWithBlock(playerID string, x, y int32, chunkPos *game.ChunkPosition, interactionType string, data map[string]string) (*game.WorldEvent, error) {
	return a.bm.InteractWithBlock(playerID, x, y, chunkPos, interactionType, data)
}
func (a *blockManagerAdapter) StartBlockUpdateLoop(ctx context.Context) {
	a.bm.StartBlockUpdateLoop(ctx)
}
func (a *blockManagerAdapter) GetBlockEvents() <-chan *game.WorldEvent {
	return a.bm.GetBlockEvents()
}

// NewWorldService создает новый экземпляр сервиса игрового мира
func NewWorldService(reg plugin.PluginRegistry) *WorldService {
	// Initialize default structured logger
	logger := zap.NewNop().Sugar()
	// Регистрируем core-системы в реестре
	reg.RegisterGameSystem(gameloop.NewTimeSystem())
	reg.RegisterGameSystem(gameloop.NewWeatherSystem(time.Now().UnixNano()))
	reg.RegisterGameSystem(gameloop.NewBlockSystem())

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	// Создаем основной объект сервиса
	ws := &WorldService{
		logger:           logger,
		registry:         reg,
		playerManager:    playermanager.NewPlayerManager(),
		chunkManager:     chunkmanager.NewChunkManager(rnd),
		clientStreams:    make(map[string]*clientConn),
		lastPosBroadcast: make(map[string]time.Time),
	}
	// Инициализируем менеджер умных блоков и регистрируем фабрики
	ws.blockManager = block.NewBlockManager(ws.chunkManager)
	// Регистрируем фабрики блоков из реестра плагинов
	for _, br := range reg.BlockFactories() {
		ws.blockManager.RegisterBlockFactory(br.BlockType, br.Factory)
	}
	// Связываем ChunkManager с BlockManager через adapter
	adapter := &blockManagerAdapter{bm: ws.blockManager}
	ws.chunkManager.SetBlockManager(adapter)
	return ws
}

// NewWorldServiceWithRegistry creates a new instance of the world service with a plugin registry and storage
func NewWorldServiceWithRegistry(reg plugin.PluginRegistry, worldStorage storage.WorldStorage) *WorldService {
	// Initialize default structured logger
	logger := zap.NewNop().Sugar()
	// Регистрируем core-системы в реестре
	reg.RegisterGameSystem(gameloop.NewTimeSystem())
	reg.RegisterGameSystem(gameloop.NewWeatherSystem(time.Now().UnixNano()))
	reg.RegisterGameSystem(gameloop.NewBlockSystem())

	// Получаем информацию о мире из хранилища
	worldInfo, err := worldStorage.LoadWorld(context.Background())
	if err == nil {
		// Если информация о мире существует, используем её сид
		rnd := rand.New(rand.NewSource(worldInfo.Seed))
		ws := &WorldService{
			logger:           logger,
			registry:         reg,
			playerManager:    playermanager.NewPlayerManager(),
			chunkManager:     chunkmanager.NewChunkManagerWithStorage(rnd, worldStorage),
			clientStreams:    make(map[string]*clientConn),
			worldStorage:     worldStorage,
			lastPosBroadcast: make(map[string]time.Time),
		}

		// Создаем менеджер блоков
		ws.blockManager = block.NewBlockManager(ws.chunkManager)
		// Регистрируем фабрики блоков из реестра плагинов
		for _, br := range reg.BlockFactories() {
			ws.blockManager.RegisterBlockFactory(br.BlockType, br.Factory)
		}
		// Связываем ChunkManager с BlockManager через adapter
		adapter := &blockManagerAdapter{bm: ws.blockManager}
		ws.chunkManager.SetBlockManager(adapter)

		return ws
	}

	return nil
}

// NewWorldServiceWithStorage creates a new instance of the world service with default plugin registry and storage (for tests compatibility).
func NewWorldServiceWithStorage(worldStorage storage.WorldStorage) *WorldService {
	return NewWorldServiceWithRegistry(plugin.NewDefaultRegistry(), worldStorage)
}

// gRPC handler methods have been moved to handlers.go
// See internal/service/handlers.go for implementations of RegisterServer, JoinGame, GameStream, GetChunks, GenerateChunks, and handleClientMessage

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
