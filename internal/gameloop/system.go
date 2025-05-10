package gameloop

import (
	"context"
	"time"

	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/playermanager"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// System описывает логику, выполняемую каждый тик цикла.
type System interface {
	// Init вызывается один раз перед запуском цикла.
	Init(deps Dependencies) error
	// Tick вызывается каждый игровой тик.
	Tick(ctx context.Context, dt time.Duration)
	// Name возвращает читаемое имя системы.
	Name() string
}

// Dependencies передаются системам при инициализации.
type Dependencies struct {
	Players *playermanager.PlayerManager
	Chunks  *chunkmanager.ChunkManager
	// EmitWorldEvent используется системами для широковещательных событий.
	EmitWorldEvent func(event *game.WorldEvent)
}
