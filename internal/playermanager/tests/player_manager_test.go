package playermanager_test

import (
	"testing"

	"github.com/annelo/go-grpc-server/internal/playermanager"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

func TestPlayerManager_AddGetRemove(t *testing.T) {
	pm := playermanager.NewPlayerManager()

	id := "player1"
	name := "Alice"
	pos := &game.Position{X: 0, Y: 0, Z: 0}

	// Add
	if err := pm.AddPlayer(id, name, pos); err != nil {
		t.Fatalf("AddPlayer returned error: %v", err)
	}

	// Duplicate add should error
	if err := pm.AddPlayer(id, name, pos); err == nil {
		t.Fatalf("expected error when adding duplicate player id")
	}

	// Get
	p, err := pm.GetPlayer(id)
	if err != nil {
		t.Fatalf("GetPlayer error: %v", err)
	}
	if p.Name != name || p.Position.X != pos.X {
		t.Fatalf("player data mismatch: got %+v", p)
	}

	// Update position
	newPos := &game.Position{X: 10, Y: 5, Z: 0}
	if err := pm.UpdatePlayerPosition(id, newPos); err != nil {
		t.Fatalf("UpdatePlayerPosition error: %v", err)
	}
	p, _ = pm.GetPlayer(id)
	if p.Position.X != newPos.X {
		t.Fatalf("position not updated")
	}

	// Remove
	if err := pm.RemovePlayer(id); err != nil {
		t.Fatalf("RemovePlayer error: %v", err)
	}

	// Get after remove should fail
	if _, err := pm.GetPlayer(id); err == nil {
		t.Fatalf("expected error after removing player")
	}
}
