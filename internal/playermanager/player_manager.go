package playermanager

import (
	"errors"
	"sync"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// PlayerData содержит информацию об игроке
type PlayerData struct {
	ID       string
	Name     string
	Position *game.Position
	Health   int32
	// Можно добавить дополнительные поля (инвентарь, характеристики и т.д.)
}

// PlayerManager управляет данными игроков
type PlayerManager struct {
	players map[string]*PlayerData
	mu      sync.RWMutex
}

// NewPlayerManager создает новый экземпляр менеджера игроков
func NewPlayerManager() *PlayerManager {
	return &PlayerManager{
		players: make(map[string]*PlayerData),
	}
}

// AddPlayer добавляет нового игрока в менеджер
func (pm *PlayerManager) AddPlayer(id, name string, position *game.Position) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Проверяем, существует ли уже игрок с таким ID
	if _, exists := pm.players[id]; exists {
		return errors.New("игрок с таким ID уже существует")
	}

	// Создаем нового игрока
	player := &PlayerData{
		ID:       id,
		Name:     name,
		Position: position,
		Health:   100, // Начальное здоровье
	}

	// Добавляем игрока в карту
	pm.players[id] = player
	return nil
}

// GetPlayer возвращает данные игрока по ID
func (pm *PlayerManager) GetPlayer(id string) (*PlayerData, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	player, exists := pm.players[id]
	if !exists {
		return nil, errors.New("игрок не найден")
	}

	return player, nil
}

// UpdatePlayerPosition обновляет позицию игрока
func (pm *PlayerManager) UpdatePlayerPosition(id string, position *game.Position) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	player, exists := pm.players[id]
	if !exists {
		return errors.New("игрок не найден")
	}

	// Обновляем позицию
	player.Position = position
	return nil
}

// UpdatePlayerHealth обновляет здоровье игрока
func (pm *PlayerManager) UpdatePlayerHealth(id string, health int32) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	player, exists := pm.players[id]
	if !exists {
		return errors.New("игрок не найден")
	}

	// Обновляем здоровье
	player.Health = health
	return nil
}

// RemovePlayer удаляет игрока из менеджера
func (pm *PlayerManager) RemovePlayer(id string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, exists := pm.players[id]; !exists {
		return errors.New("игрок не найден")
	}

	// Удаляем игрока из карты
	delete(pm.players, id)
	return nil
}

// GetAllPlayers возвращает список всех игроков
func (pm *PlayerManager) GetAllPlayers() []*PlayerData {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	players := make([]*PlayerData, 0, len(pm.players))
	for _, player := range pm.players {
		players = append(players, player)
	}

	return players
}
