package gameloop

import (
	"context"
	"log"
	"time"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// TimeSystem отвечает за ход игрового времени.
type TimeSystem struct {
	deps  Dependencies
	ticks int64
	day   int32
}

const (
	ticksPerDay    = 1200 // ~60 секунд при 20 TPS
	broadcastEvery = 100  // каждые 5 секунд
)

func NewTimeSystem() *TimeSystem { return &TimeSystem{} }

func (t *TimeSystem) Name() string { return "time" }

func (t *TimeSystem) Init(deps Dependencies) error {
	t.deps = deps
	return nil
}

func (t *TimeSystem) Tick(ctx context.Context, dt time.Duration) {
	// Один Tick цикла == 1 игровой тик
	t.ticks++
	if t.ticks%ticksPerDay == 0 {
		t.day++
	}

	// Периодически оповещаем клиентов
	if t.ticks%broadcastEvery == 0 {
		log.Printf("[TimeSystem.Tick] broadcasting TIME_CHANGED at tick=%d (dayTime=%d, day=%d)", t.ticks, t.ticks%ticksPerDay, t.day)
		if t.deps.EmitWorldEvent != nil {
			ti := &game.TimeInfo{
				DayTime: t.ticks % ticksPerDay,
				Day:     t.day,
			}
			evt := &game.WorldEvent{
				Type: game.WorldEvent_TIME_CHANGED,
				Payload: &game.WorldEvent_TimeInfo{
					TimeInfo: ti,
				},
			}
			t.deps.EmitWorldEvent(evt)
		}
	}
}
