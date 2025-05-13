package service

import (
	"context"
	"expvar"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/plugin"
	"github.com/annelo/go-grpc-server/internal/storage"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RegisterServer registers the WorldService on the given gRPC server.
func (s *WorldService) RegisterServer(grpcServer *grpc.Server) {
	game.RegisterWorldServiceServer(grpcServer, s)
}

// JoinGame handles player join requests.
func (s *WorldService) JoinGame(ctx context.Context, req *game.JoinRequest) (*game.JoinResponse, error) {
	playerID := uuid.New().String()
	spawnPos := &game.Position{X: 1000.0, Y: 1000.0, Z: 0}

	if err := s.playerManager.AddPlayer(playerID, req.PlayerName, spawnPos); err != nil {
		s.logger.Errorf("Failed to add player %s: %v", req.PlayerName, err)
		return &game.JoinResponse{Success: false, ErrorMessage: fmt.Sprintf("Could not add player: %v", err)}, nil
	}

	s.logger.Infof("Игрок %s (%s) присоединился к игре", req.PlayerName, playerID)
	expvar.Get("players_connected").(*expvar.Int).Add(1)

	// Attempt to load saved state if storage is available
	if s.worldStorage != nil {
		if ps, err := s.worldStorage.LoadPlayerState(ctx, playerID); err == nil {
			if player, err := s.playerManager.GetPlayer(playerID); err == nil {
				player.Position = ps.Position
				player.Health = ps.Health
			}
		}
	}

	return &game.JoinResponse{PlayerId: playerID, SpawnPosition: spawnPos, Success: true}, nil
}

// GameStream establishes a bidirectional stream for game events.
func (s *WorldService) GameStream(stream game.WorldService_GameStreamServer) error {
	// Receive first message to identify player
	clientMsg, err := stream.Recv()
	if err != nil {
		s.logger.Errorf("Error receiving first message: %v", err)
		return status.Errorf(codes.Internal, "Error receiving first message: %v", err)
	}

	playerID := clientMsg.PlayerId
	player, err := s.playerManager.GetPlayer(playerID)
	if err != nil {
		s.logger.Warnf("Player not found: %v", err)
		return status.Errorf(codes.NotFound, "Player not found: %v", err)
	}

	s.mu.Lock()
	conn := &clientConn{stream: stream, highQueue: make(chan *game.ServerMessage, sendQueueSize), normalQueue: make(chan *game.ServerMessage, sendQueueSize)}
	s.clientStreams[playerID] = conn
	s.mu.Unlock()

	s.broadcastPlayerUpdate(playerID, player.Position, true)

	// Sender goroutine: priority queue
	go func() {
		for {
			var msg *game.ServerMessage
			select {
			case msg = <-conn.highQueue:
			default:
				select {
				case msg = <-conn.highQueue:
				case msg = <-conn.normalQueue:
				}
			}
			if err := conn.stream.Send(msg); err != nil {
				s.logger.Warnf("Send to client %s error: %v", playerID, err)
				return
			}
		}
	}()

	// Handle incoming messages
	for {
		clientMsg, err := stream.Recv()
		if err != nil {
			s.logger.Infof("Connection lost for player %s: %v", playerID, err)
			break
		}
		s.handleClientMessage(clientMsg, stream)
	}

	s.mu.Lock()
	if c, ok := s.clientStreams[playerID]; ok {
		close(c.highQueue)
		close(c.normalQueue)
		delete(s.clientStreams, playerID)
	}
	s.mu.Unlock()

	s.broadcastPlayerUpdate(playerID, nil, false)
	expvar.Get("players_connected").(*expvar.Int).Add(-1)

	if s.worldStorage != nil {
		_ = s.worldStorage.SavePlayerState(context.Background(), &storage.PlayerState{ID: playerID, Name: player.Name, Position: player.Position, Health: player.Health, LastSeen: time.Now().Unix()})
	}

	s.logger.Infof("Player %s disconnected", playerID)
	return nil
}

// GetChunks handles world chunk requests.
func (s *WorldService) GetChunks(req *game.ChunkRequest, stream game.WorldService_GetChunksServer) error {
	player, err := s.playerManager.GetPlayer(req.PlayerId)
	if err != nil {
		s.logger.Warnf("Player not found for GetChunks: %v", err)
		return status.Errorf(codes.NotFound, "Player not found: %v", err)
	}

	s.logger.Infof("GetChunks request from %s pos=[%.1f,%.1f] radius=%d", player.Name, req.PlayerPosition.X, req.PlayerPosition.Y, req.Radius)

	radius := req.Radius
	if radius > 2 {
		radius = 2
		s.logger.Debugf("Radius reduced to %d for player %s", radius, req.PlayerId)
	}

	playerChunkX := int32(req.PlayerPosition.X) / chunkmanager.ChunkSize
	playerChunkY := int32(req.PlayerPosition.Y) / chunkmanager.ChunkSize

	for y := playerChunkY - radius; y <= playerChunkY+radius; y++ {
		for x := playerChunkX - radius; x <= playerChunkX+radius; x++ {
			chunkPos := &game.ChunkPosition{X: x, Y: y}
			chunk, err := s.chunkManager.GetChunkSnapshot(chunkPos)
			if err != nil {
				s.logger.Warnf("Error getting chunk [%d,%d]: %v", x, y, err)
				continue
			}
			if err := stream.Send(chunk); err != nil {
				s.logger.Errorf("Error sending chunk [%d,%d]: %v", x, y, err)
				return status.Errorf(codes.Internal, "Error sending chunk: %v", err)
			}
		}
	}
	return nil
}

// GenerateChunks generates world chunks on demand.
func (s *WorldService) GenerateChunks(ctx context.Context, req *game.GenerateRequest) (*game.GenerateResponse, error) {
	if _, err := s.playerManager.GetPlayer(req.PlayerId); err != nil {
		return &game.GenerateResponse{Success: false, ErrorMessage: fmt.Sprintf("Player not found: %v", err)}, nil
	}
	for _, pos := range req.Positions {
		for _, h := range s.registry.Hooks(plugin.HookBeforeChunkGenerate) {
			h(pos)
		}
		if _, err := s.chunkManager.GetOrGenerateChunk(pos); err != nil {
			s.logger.Warnf("Error generating chunk [%d,%d]: %v", pos.X, pos.Y, err)
			return &game.GenerateResponse{Success: false, ErrorMessage: fmt.Sprintf("Error generating chunk [%d,%d]: %v", pos.X, pos.Y, err)}, nil
		}
		for _, h := range s.registry.Hooks(plugin.HookAfterChunkGenerate) {
			h(pos)
		}
	}
	return &game.GenerateResponse{Success: true}, nil
}

// handleClientMessage routes incoming client messages to appropriate handlers.
func (s *WorldService) handleClientMessage(msg *game.ClientMessage, stream game.WorldService_GameStreamServer) {
	switch payload := msg.Payload.(type) {
	case *game.ClientMessage_Movement:
		s.handlePlayerMovement(msg.PlayerId, payload.Movement)
	case *game.ClientMessage_BlockAction:
		s.handleBlockAction(msg.PlayerId, payload.BlockAction)
	case *game.ClientMessage_Chat:
		s.handleChatMessage(msg.PlayerId, payload.Chat)
	case *game.ClientMessage_Ping:
		s.handlePing(msg.PlayerId, payload.Ping)
	}
}
