package plugin_test

import (
	"sync"
	"testing"

	"github.com/annelo/go-grpc-server/internal/plugin"
	"github.com/stretchr/testify/assert"
)

func TestPluginManager_UnloadPlugins_CallsHooks(t *testing.T) {
	reg := plugin.NewDefaultRegistry()
	// Register plugin metadata entries
	meta1 := plugin.PluginMeta{Name: "p1", Version: plugin.PluginAPIVersion}
	meta2 := plugin.PluginMeta{Name: "p2", Version: plugin.PluginAPIVersion}
	reg.RegisterPluginMeta(meta1)
	reg.RegisterPluginMeta(meta2)

	// Track hook invocations
	calledBefore := []string{}
	calledAfter := []string{}
	reg.RegisterHook(plugin.HookBeforePluginUnload, func(args ...interface{}) {
		if m, ok := args[0].(plugin.PluginMeta); ok {
			calledBefore = append(calledBefore, m.Name)
		}
	})
	reg.RegisterHook(plugin.HookAfterPluginUnload, func(args ...interface{}) {
		if m, ok := args[0].(plugin.PluginMeta); ok {
			calledAfter = append(calledAfter, m.Name)
		}
	})

	pm := plugin.NewPluginManager("")
	pm.UnloadPlugins(reg)

	// Expect before hooks for each plugin in order
	assert.ElementsMatch(t, []string{"p1", "p2"}, calledBefore, "expected before unload hooks for all plugins")
	// Expect after hooks for each plugin in order
	assert.ElementsMatch(t, []string{"p1", "p2"}, calledAfter, "expected after unload hooks for all plugins")
}

func TestPluginManager_LoadPlugins_InvalidDir_ReturnsError(t *testing.T) {
	pm := plugin.NewPluginManager("/nonexistent_directory_for_tests")
	err := pm.LoadPlugins(plugin.NewDefaultRegistry())
	assert.Error(t, err, "expected error loading plugins from invalid directory")
}

// Test concurrent LoadPlugins calls to ensure no data races and no deadlocks
func TestPluginManager_ConcurrentLoad(t *testing.T) {
	pm := plugin.NewPluginManager(t.TempDir())
	reg := plugin.NewDefaultRegistry()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = pm.LoadPlugins(reg)
	}()
	go func() {
		defer wg.Done()
		_ = pm.LoadPlugins(reg)
	}()
	wg.Wait()
}
