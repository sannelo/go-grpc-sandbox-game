package service

import (
	"time"

	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

const (
	// Minimum interval between player position broadcasts.
	playerBroadcastMinInterval = 200 * time.Millisecond
	// Radius of visibility in chunks.
	visibilityChunkRadius = 8
)

// broadcastPlayerUpdate sends player position updates to clients with throttling.
func (s *WorldService) broadcastPlayerUpdate(playerID string, position *game.Position, isConnected bool) {
	s.throttleMu.Lock()
	last, ok := s.lastPosBroadcast[playerID]
	if ok && time.Since(last) < playerBroadcastMinInterval {
		s.throttleMu.Unlock()
		return
	}
	s.lastPosBroadcast[playerID] = time.Now()
	s.throttleMu.Unlock()

	player, err := s.playerManager.GetPlayer(playerID)
	if err != nil {
		s.logger.Warnf("Failed to find player %s for update: %v", playerID, err)
		return
	}

	update := &game.PlayerUpdate{
		PlayerId:    playerID,
		Position:    position,
		Health:      player.Health,
		IsConnected: isConnected,
	}

	if position == nil {
		s.broadcastToAll(&game.ServerMessage{Payload: &game.ServerMessage_PlayerUpdate{PlayerUpdate: update}})
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for targetID, conn := range s.clientStreams {
		tgt, err := s.playerManager.GetPlayer(targetID)
		if err != nil || tgt.Position == nil {
			continue
		}
		dx := int32(position.X-tgt.Position.X) / chunkmanager.ChunkSize
		dy := int32(position.Y-tgt.Position.Y) / chunkmanager.ChunkSize
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx > visibilityChunkRadius || dy > visibilityChunkRadius {
			continue
		}
		conn.send(&game.ServerMessage{Payload: &game.ServerMessage_PlayerUpdate{PlayerUpdate: update}}, false)
	}
}

// broadcastWorldEvent sends world events to clients; global events are non-blocking and sent to all.
func (s *WorldService) broadcastWorldEvent(event *game.WorldEvent) {
	msg := &game.ServerMessage{Payload: &game.ServerMessage_WorldEvent{WorldEvent: event}}
	switch event.Type {
	case game.WorldEvent_WEATHER_CHANGED, game.WorldEvent_TIME_CHANGED, game.WorldEvent_SERVER_SHUTDOWN:
		s.logger.Debugf("Global world event: %v", event.Type)
		s.mu.RLock()
		for _, conn := range s.clientStreams {
			conn.send(msg, false)
		}
		s.mu.RUnlock()
		return
	}

	if event.Position == nil {
		s.broadcastToAll(msg)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for playerID, conn := range s.clientStreams {
		player, err := s.playerManager.GetPlayer(playerID)
		if err != nil || player.Position == nil {
			continue
		}
		dx := int32(event.Position.X-player.Position.X) / chunkmanager.ChunkSize
		dy := int32(event.Position.Y-player.Position.Y) / chunkmanager.ChunkSize
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx > visibilityChunkRadius || dy > visibilityChunkRadius {
			continue
		}
		conn.send(msg, false)
	}
}

// broadcastToAll sends a message to all connected clients.
func (s *WorldService) broadcastToAll(msg *game.ServerMessage) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, conn := range s.clientStreams {
		conn.send(msg, false)
	}
}

// sendMessageToPlayer sends a chat message to a specific player.
func (s *WorldService) sendMessageToPlayer(playerID string, message string) {
	s.mu.RLock()
	conn, exists := s.clientStreams[playerID]
	s.mu.RUnlock()
	if !exists {
		return
	}
	chatMsg := &game.ChatBroadcast{
		PlayerId:   "server",
		PlayerName: "Server",
		Content:    message,
		IsGlobal:   false,
	}
	conn.send(&game.ServerMessage{Payload: &game.ServerMessage_ChatBroadcast{ChatBroadcast: chatMsg}}, false)
}
