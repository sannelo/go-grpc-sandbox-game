package service

import (
	"context"
	"time"

	"github.com/annelo/go-grpc-server/internal/gameloop"
)

// Start initializes and runs all world service routines, including storage loading, block updates, and the game loop.
func (s *WorldService) Start(ctx context.Context) {
	if s.chunkManager.HasStorage() {
		s.logger.Info("Loading chunks from storage...")
		s.chunkManager.StartPeriodicalSaving(ctx, 30*time.Second)
		s.chunkManager.StartCacheCleanupRoutine(ctx, 2*time.Minute)
		if err := s.chunkManager.LoadChunksFromStorage(ctx); err != nil {
			s.logger.Warnf("Error loading chunks from storage: %v", err)
		} else {
			s.logger.Info("Chunks successfully loaded from storage")
		}
	}

	// Start background routines
	go s.monitorCacheUsage(ctx)
	go s.blockManager.StartBlockUpdateLoop(ctx)
	go s.processBlockEvents(ctx)

	// Initialize and start game loop with registered systems
	deps := gameloop.Dependencies{
		Players:        s.playerManager,
		Chunks:         s.chunkManager,
		EmitWorldEvent: s.broadcastWorldEvent,
	}
	systems := s.registry.GameSystems()
	s.loop = gameloop.NewLoop(50*time.Millisecond, deps, systems...)
	go s.loop.Run(ctx)
}

// monitorCacheUsage periodically logs cache usage statistics.
func (s *WorldService) monitorCacheUsage(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			stats := s.chunkManager.GetCacheStats()
			s.logger.Infof("Cache stats: %+v", stats)
		case <-ctx.Done():
			return
		}
	}
}

// processBlockEvents receives block events and broadcasts them to clients.
func (s *WorldService) processBlockEvents(ctx context.Context) {
	blockEvents := s.blockManager.GetBlockEvents()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-blockEvents:
			s.broadcastWorldEvent(event)
		}
	}
}
