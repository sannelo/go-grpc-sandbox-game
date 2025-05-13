package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/annelo/go-grpc-server/internal/block"
	"github.com/annelo/go-grpc-server/internal/gameloop"
	"github.com/annelo/go-grpc-server/internal/plugin"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
	"github.com/stretchr/testify/assert"
)

func TestDefaultRegistry_RegisterAndRetrieve(t *testing.T) {
	reg := plugin.NewDefaultRegistry()

	// Block factory registration
	reg.RegisterBlockFactory(42, block.NewStaticBlock)
	blocks := reg.BlockFactories()
	assert.Len(t, blocks, 1, "expected one block factory")
	assert.Equal(t, int32(42), blocks[0].BlockType)
	assert.NotNil(t, blocks[0].Factory)

	// Game system registration
	timeSys := gameloop.NewTimeSystem()
	reg.RegisterGameSystem(timeSys)
	systems := reg.GameSystems()
	assert.Len(t, systems, 1, "expected one game system")
	assert.Equal(t, timeSys, systems[0])

	// Plugin metadata registration
	meta := plugin.PluginMeta{Name: "test-plugin", Version: "1.0.0", Author: "ai", Description: "desc"}
	reg.RegisterPluginMeta(meta)
	metas := reg.PluginMetas()
	assert.Len(t, metas, 1, "expected one plugin meta")
	assert.Equal(t, meta, metas[0])

	// Hook registration and invocation
	called := false
	hookFunc := func(args ...interface{}) {
		// Expecting position argument for BeforeChunkGenerate
		if len(args) == 1 {
			if pos, ok := args[0].(*game.ChunkPosition); ok {
				assert.Equal(t, &game.ChunkPosition{X: 1, Y: 2}, pos)
			}
		}
		called = true
	}
	reg.RegisterHook(plugin.HookBeforeChunkGenerate, hookFunc)
	hooks := reg.Hooks(plugin.HookBeforeChunkGenerate)
	assert.Len(t, hooks, 1, "expected one hook for BeforeChunkGenerate")

	// Invoke
	hooks[0](&game.ChunkPosition{X: 1, Y: 2})
	assert.True(t, called, "hook should have been called")

	// Command registration and invocation
	calledCmd := false
	cmdFunc := func(args []string) (string, error) {
		calledCmd = true
		return "out: " + strings.Join(args, ","), nil
	}
	reg.RegisterCommand("testcmd", "test command", cmdFunc)
	cmds := reg.Commands()
	assert.Len(t, cmds, 1, "expected one command")
	assert.Equal(t, "testcmd", cmds[0].Name)
	assert.Equal(t, "test command", cmds[0].Description)
	out, err := cmds[0].Handler([]string{"a", "b"})
	assert.NoError(t, err)
	assert.Equal(t, "out: a,b", out)
	assert.True(t, calledCmd, "command handler should have been called")
}

func TestDefaultRegistry_MarkCoreAndClearPlugins(t *testing.T) {
	reg := plugin.NewDefaultRegistry()

	// Core registrations
	reg.RegisterBlockFactory(1, block.NewStaticBlock)
	timeSys := gameloop.NewTimeSystem()
	reg.RegisterGameSystem(timeSys)
	coreMeta := plugin.PluginMeta{Name: "core", Version: plugin.PluginAPIVersion}
	reg.RegisterPluginMeta(coreMeta)
	hookFunc := func(args ...interface{}) {}
	reg.RegisterHook(plugin.HookBeforeChunkGenerate, hookFunc)
	cmdFunc := func(args []string) (string, error) { return "", nil }
	reg.RegisterCommand("corecmd", "core command", cmdFunc)

	// Mark core boundary
	reg.MarkCore()

	// Plugin additions
	reg.RegisterBlockFactory(2, block.NewStaticBlock)
	reg.RegisterGameSystem(gameloop.NewWeatherSystem(0))
	reg.RegisterPluginMeta(plugin.PluginMeta{Name: "p1", Version: plugin.PluginAPIVersion})
	reghook := func(args ...interface{}) {}
	reg.RegisterHook(plugin.HookAfterChunkGenerate, reghook)
	reg.RegisterCommand("plugincmd", "plugin command", cmdFunc)

	// Before clear assertions
	assert.Len(t, reg.BlockFactories(), 2)
	assert.Len(t, reg.GameSystems(), 2)
	assert.Len(t, reg.PluginMetas(), 2)
	assert.Len(t, reg.Hooks(plugin.HookBeforeChunkGenerate), 1)
	assert.Len(t, reg.Hooks(plugin.HookAfterChunkGenerate), 1)
	assert.Len(t, reg.Commands(), 2)

	// Clear plugin registrations
	reg.ClearPlugins()

	// After clear assertions
	assert.Len(t, reg.BlockFactories(), 1)
	assert.Len(t, reg.GameSystems(), 1)
	assert.Len(t, reg.PluginMetas(), 1)
	assert.Len(t, reg.Hooks(plugin.HookBeforeChunkGenerate), 1)
	assert.Len(t, reg.Hooks(plugin.HookAfterChunkGenerate), 0)
	assert.Len(t, reg.Commands(), 1)
}

// Test loading of plugin configuration YAML into registry
func TestDefaultRegistry_LoadPluginConfig(t *testing.T) {
	type SampleConfig struct {
		Value int    `yaml:"value"`
		Name  string `yaml:"name"`
	}
	reg := plugin.NewDefaultRegistry()
	// Register config sample before loading
	reg.RegisterPluginConfig("testplugin", &SampleConfig{})
	// Prepare temp dir and YAML file
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "testplugin.yaml")
	content := []byte("value: 42\nname: hello")
	if err := os.WriteFile(cfgPath, content, 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	// Load config
	err := reg.LoadPluginConfig("testplugin", dir)
	assert.NoError(t, err)
	// Retrieve loaded config
	cfg := reg.PluginConfig("testplugin")
	sc, ok := cfg.(*SampleConfig)
	assert.True(t, ok, "expected SampleConfig pointer")
	assert.Equal(t, 42, sc.Value)
	assert.Equal(t, "hello", sc.Name)
}

// Test concurrent registration and retrieval to ensure thread-safety
func TestDefaultRegistry_ConcurrentAccess(t *testing.T) {
	reg := plugin.NewDefaultRegistry()
	const N = 100
	var wg sync.WaitGroup
	// Concurrent registrations
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reg.RegisterBlockFactory(int32(i), block.NewStaticBlock)
		}(i)
	}
	// Concurrent getters
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = reg.BlockFactories()
		}()
	}
	wg.Wait()
	blocks := reg.BlockFactories()
	assert.Len(t, blocks, N, "expected %d block factories after concurrent registration, got %d", N, len(blocks))
}
