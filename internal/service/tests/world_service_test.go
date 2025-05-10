package service_test

import (
	"context"
	"testing"

	"github.com/annelo/go-grpc-server/internal/service"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

func TestWorldService_JoinGame(t *testing.T) {
	ws := service.NewWorldService()

	resp, err := ws.JoinGame(context.Background(), &game.JoinRequest{PlayerName: "Tester"})
	if err != nil {
		t.Fatalf("JoinGame returned error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success true, got false: %s", resp.ErrorMessage)
	}
	if resp.PlayerId == "" {
		t.Fatalf("playerId should not be empty")
	}
}
