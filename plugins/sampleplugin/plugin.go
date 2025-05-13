package main

import (
	"fmt"
	"log"

	"github.com/annelo/go-grpc-server/internal/block"
	"github.com/annelo/go-grpc-server/internal/gameloop"
	"github.com/annelo/go-grpc-server/internal/plugin"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// Register is invoked by PluginManager to register block factories and systems
func Register(reg plugin.PluginRegistry) {
	// Register a custom static block factory for blockType 100
	reg.RegisterBlockFactory(100, func(x, y int32, chunkPos *game.ChunkPosition, blockType int32) block.Block {
		// For demonstration, reuse the static block factory
		return block.NewStaticBlock(x, y, chunkPos, blockType)
	})

	// Register a custom game system (time system)
	reg.RegisterGameSystem(gameloop.NewTimeSystem())

	// Sample plugin hook: log when a block is placed
	reg.RegisterHook(plugin.HookAfterPlaceBlock, func(args ...interface{}) {
		if len(args) == 1 {
			if event, ok := args[0].(*game.WorldEvent); ok {
				log.Printf("[SamplePlugin] Block placed event: %+v", event)
			}
		}
	})

	// Sample plugin configuration structure
	type SamplePluginConfig struct {
		Greeting string `yaml:"greeting"`
		Value    int    `yaml:"value"`
	}
	// Register sample config for this plugin
	reg.RegisterPluginConfig("sampleplugin", &SamplePluginConfig{})

	// Sample plugin CLI command: show plugin info
	reg.RegisterCommand("sampleinfo", "Show sample plugin info", func(args []string) (string, error) {
		// Access loaded config
		cfg := reg.PluginConfig("sampleplugin").(*SamplePluginConfig)
		return fmt.Sprintf("Greeting: %s, Value: %d\n", cfg.Greeting, cfg.Value), nil
	})
}
