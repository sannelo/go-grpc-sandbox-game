package service

import (
	"context"
	"log"
	"time"

	"github.com/annelo/go-grpc-server/internal/storage"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// Stop saves all player and chunk data and shuts down the service.
func (s *WorldService) Stop() {
	// Disconnect all clients cleanly
	s.DisconnectAllClients()

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
		// Save all chunks
		s.chunkManager.SaveChunksToStorage(context.Background())
		s.worldStorage.Close()
	}
}

// DisconnectAllClients disconnects all clients by sending a shutdown event and closing queues.
func (s *WorldService) DisconnectAllClients() {
	log.Println("Disconnecting all clients...")

	shutdownMsg := &game.ServerMessage{
		Payload: &game.ServerMessage_WorldEvent{
			WorldEvent: &game.WorldEvent{
				Type:    game.WorldEvent_SERVER_SHUTDOWN,
				Message: "Server is shutting down",
			},
		},
	}

	s.mu.Lock()
	count := len(s.clientStreams)
	for _, conn := range s.clientStreams {
		conn.send(shutdownMsg, true)
		close(conn.highQueue)
		close(conn.normalQueue)
	}
	s.clientStreams = make(map[string]*clientConn)
	s.mu.Unlock()

	log.Printf("Disconnected %d clients", count)
}
