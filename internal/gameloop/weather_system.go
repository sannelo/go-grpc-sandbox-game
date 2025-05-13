package gameloop

import (
	"context"
	"log"
	"math/rand"
	"time"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// WeatherSystem случайным образом меняет погоду.
type WeatherSystem struct {
	deps           Dependencies
	rng            *rand.Rand
	currentWeather game.Weather_WeatherType
	ticksRemaining int64
}

func NewWeatherSystem(seed int64) *WeatherSystem {
	return &WeatherSystem{
		rng: rand.New(rand.NewSource(seed)),
	}
}

func (w *WeatherSystem) Name() string { return "weather" }

func (w *WeatherSystem) Init(deps Dependencies) error {
	w.deps = deps
	w.currentWeather = game.Weather_CLEAR
	w.ticksRemaining = w.randomDuration()
	return nil
}

func (w *WeatherSystem) Tick(ctx context.Context, dt time.Duration) {
	w.ticksRemaining--
	if w.ticksRemaining > 0 {
		// DEBUG: no change this tick
		return
	}
	// Выбираем новую погоду (чтобы не повторять ту же)
	newWeather := w.currentWeather
	for newWeather == w.currentWeather {
		r := w.rng.Float64()
		switch {
		case r < 0.7:
			newWeather = game.Weather_CLEAR
		case r < 0.9:
			newWeather = game.Weather_RAIN
		default:
			newWeather = game.Weather_STORM
		}
	}
	w.currentWeather = newWeather
	w.ticksRemaining = w.randomDuration()
	log.Printf("[WeatherSystem.Tick] broadcasting WEATHER_CHANGED newWeather=%v, nextDuration=%d", newWeather, w.ticksRemaining)

	if w.deps.EmitWorldEvent != nil {
		evt := &game.WorldEvent{
			Type: game.WorldEvent_WEATHER_CHANGED,
			Payload: &game.WorldEvent_Weather{
				Weather: &game.Weather{
					Type:      newWeather,
					Intensity: w.rng.Float32(),
				},
			},
		}
		w.deps.EmitWorldEvent(evt)
	}
}

func (w *WeatherSystem) randomDuration() int64 {
	// От 300 до 900 тиков (~15–45 сек)
	return int64(300 + w.rng.Intn(600))
}
