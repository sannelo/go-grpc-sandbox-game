package plugin_test

import (
	"path/filepath"
	"testing"

	"github.com/annelo/go-grpc-server/internal/plugin"
	"github.com/stretchr/testify/assert"
)

func TestIntegration_SamplePlugin(t *testing.T) {
	reg := plugin.NewDefaultRegistry()
	// Plugin directory relative to this test file
	pluginDir := filepath.Join("..", "..", "plugins", "sampleplugin")
	pm := plugin.NewPluginManager(pluginDir)
	// Mark core before loading plugins
	reg.MarkCore()
	err := pm.LoadPlugins(reg)
	assert.NoError(t, err)

	// Plugin metadata loaded
	metas := reg.PluginMetas()
	assert.Len(t, metas, 1, "expected one plugin metadata")
	assert.Equal(t, "sampleplugin", metas[0].Name)

	// Block factory for type 100
	foundBlock := false
	for _, b := range reg.BlockFactories() {
		if b.BlockType == 100 {
			foundBlock = true
		}
	}
	assert.True(t, foundBlock, "expected block factory for type 100")

	// Hooks for block placement
	hooks := reg.Hooks(plugin.HookAfterPlaceBlock)
	assert.NotEmpty(t, hooks, "expected at least one hook for HookAfterPlaceBlock")

	// CLI commands
	cmds := reg.Commands()
	cmdMap := make(map[string]plugin.CommandRegistration)
	for _, c := range cmds {
		cmdMap[c.Name] = c
	}

	// sampleinfo command
	sinfo, ok := cmdMap["sampleinfo"]
	assert.True(t, ok, "expected sampleinfo command")
	out, err := sinfo.Handler(nil)
	assert.NoError(t, err)
	assert.Contains(t, out, "Greeting: Hello from SamplePlugin")
	assert.Contains(t, out, "Value: 123")

	// Plugin config should be loaded
	cfg := reg.PluginConfig("sampleplugin")
	assert.NotNil(t, cfg, "expected plugin config to be loaded")
}
