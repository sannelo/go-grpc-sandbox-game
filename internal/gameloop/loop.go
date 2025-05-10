package gameloop

import (
	"context"
	"log"
	"time"
)

// Loop — главный цикл, вызывающий Tick всех зарегистрированных систем.
type Loop struct {
	systems []System
	tickDur time.Duration
}

// NewLoop создаёт цикл с заданной длительностью тика.
func NewLoop(tick time.Duration, deps Dependencies, systems ...System) *Loop {
	// Инициализируем все системы
	for _, s := range systems {
		if err := s.Init(deps); err != nil {
			log.Printf("[GameLoop] init %s error: %v", s.Name(), err)
		}
	}
	return &Loop{systems: systems, tickDur: tick}
}

// Run запускает бесконечный цикл до отмены ctx.
func (l *Loop) Run(ctx context.Context) {
	ticker := time.NewTicker(l.tickDur)
	defer ticker.Stop()

	last := time.Now()
	for {
		select {
		case t := <-ticker.C:
			dt := t.Sub(last)
			last = t
			for _, s := range l.systems {
				func(sys System) {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("[GameLoop] panic in %s: %v", sys.Name(), r)
						}
					}()
					sys.Tick(ctx, dt)
				}(s)
			}
		case <-ctx.Done():
			log.Println("[GameLoop] stopped")
			return
		}
	}
}
