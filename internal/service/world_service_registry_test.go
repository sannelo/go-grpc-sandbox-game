package service_test

import (
	"testing"

	"github.com/annelo/go-grpc-server/internal/gameloop"
	"github.com/annelo/go-grpc-server/internal/plugin"
	"github.com/annelo/go-grpc-server/internal/service"
	"github.com/stretchr/testify/assert"
)

func TestNewWorldService_RegistersCoreSystems(t *testing.T) {
	reg := plugin.NewDefaultRegistry()
	ws := service.NewWorldService(reg)
	assert.NotNil(t, ws, "WorldService should not be nil")

	systems := reg.GameSystems()
	assert.Len(t, systems, 3, "expected 3 core systems registered")
	assert.IsType(t, gameloop.NewTimeSystem(), systems[0], "first system should be TimeSystem")
	assert.IsType(t, gameloop.NewWeatherSystem(0), systems[1], "second system should be WeatherSystem")
	assert.IsType(t, gameloop.NewBlockSystem(), systems[2], "third system should be BlockSystem")
}
