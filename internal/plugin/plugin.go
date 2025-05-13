package plugin

import (
	"encoding/json"
	"expvar"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	pluginpkg "plugin"
	"reflect"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/annelo/go-grpc-server/internal/block"
	"github.com/annelo/go-grpc-server/internal/gameloop"
)

// PluginMeta holds metadata for a plugin
type PluginMeta struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
}

// HookType defines a named event hook
type HookType string

// Common hook types
const (
	HookBeforeChunkGenerate HookType = "BeforeChunkGenerate"
	HookAfterChunkGenerate  HookType = "AfterChunkGenerate"
	HookBeforeSaveChunk     HookType = "BeforeSaveChunk"
	HookAfterSaveChunk      HookType = "AfterSaveChunk"
	HookBeforePlaceBlock    HookType = "BeforePlaceBlock"
	HookAfterPlaceBlock     HookType = "AfterPlaceBlock"
	HookBeforeRemoveBlock   HookType = "BeforeRemoveBlock"
	HookAfterRemoveBlock    HookType = "AfterRemoveBlock"
	// Plugin load/unload hook types
	HookBeforePluginLoad   HookType = "BeforePluginLoad"
	HookAfterPluginLoad    HookType = "AfterPluginLoad"
	HookBeforePluginUnload HookType = "BeforePluginUnload"
	HookAfterPluginUnload  HookType = "AfterPluginUnload"
)

// HookFunc is the signature for hook handlers. args can be event-specific.
type HookFunc func(args ...interface{})

// CommandFunc is the signature for admin CLI command handlers.
type CommandFunc func(args []string) (string, error)

// CommandRegistration holds a single CLI command registration.
type CommandRegistration struct {
	// Name is the command name.
	Name string
	// Description is a brief help text for the command.
	Description string
	// Handler executes the command logic.
	Handler CommandFunc
}

// PluginRegistry allows registration of block factories and game systems.
type PluginRegistry interface {
	// RegisterBlockFactory registers a factory for a given block type.
	RegisterBlockFactory(blockType int32, factory block.BlockFactory)
	// RegisterGameSystem registers a game loop system to be ticked every tick.
	RegisterGameSystem(sys gameloop.System)
	// BlockFactories returns all registered block factories.
	BlockFactories() []BlockRegistration
	// GameSystems returns all registered game loop systems.
	GameSystems() []gameloop.System
	// RegisterPluginMeta registers metadata for a plugin.
	RegisterPluginMeta(meta PluginMeta)
	// PluginMetas returns all registered plugin metadata.
	PluginMetas() []PluginMeta
	// RegisterHook registers a hook handler for a given hook type.
	RegisterHook(hook HookType, fn HookFunc)
	// Hooks returns all handlers registered for a hook type.
	Hooks(hook HookType) []HookFunc
	// RegisterCommand registers an admin CLI command.
	RegisterCommand(name, description string, handler CommandFunc)
	// Commands returns all registered admin CLI commands.
	Commands() []CommandRegistration
	// MarkCore marks the boundary between core and plugin registrations.
	MarkCore()
	// ClearPlugins removes all registrations added after MarkCore.
	ClearPlugins()
	// RegisterPluginConfig registers a sample config struct for a plugin.
	RegisterPluginConfig(name string, sample interface{})
	// LoadPluginConfig loads a plugin's config YAML from the given directory into the registry.
	LoadPluginConfig(name, dir string) error
	// PluginConfig returns the loaded config object for a plugin.
	PluginConfig(name string) interface{}
}

// BlockRegistration holds a single block factory registration.
type BlockRegistration struct {
	// BlockType is the numeric ID for the block.
	BlockType int32
	// Factory is the function to create the block.
	Factory block.BlockFactory
}

// DefaultRegistry is the default implementation of PluginRegistry.
type DefaultRegistry struct {
	// blockFactories is a list of all registered block factories.
	blockFactories []BlockRegistration
	// gameSystems is a list of all registered game loop systems.
	gameSystems []gameloop.System
	// pluginMetas holds metadata for loaded plugins.
	pluginMetas []PluginMeta
	// commands holds registered admin CLI commands.
	commands []CommandRegistration
	// hooks holds registered hook handlers.
	hooks map[HookType][]HookFunc
	// configSamples maps plugin name to a sample config struct pointer.
	configSamples map[string]interface{}
	// configs maps plugin name to the loaded config object pointer.
	configs map[string]interface{}
	// mu protects all registry data structures for concurrent access.
	mu sync.RWMutex
	// coreBlockCount is the number of blockFactories at core mark.
	coreBlockCount int
	// coreSystemCount is the number of gameSystems at core mark.
	coreSystemCount int
	// coreCommandCount is the number of commands at core mark.
	coreCommandCount int
	// corePluginMetaCount is the number of pluginMetas at core mark.
	corePluginMetaCount int
	// coreHooks is a snapshot of hooks at core mark.
	coreHooks map[HookType][]HookFunc
}

// NewDefaultRegistry returns a new DefaultRegistry instance.
func NewDefaultRegistry() *DefaultRegistry {
	return &DefaultRegistry{
		hooks:         make(map[HookType][]HookFunc),
		configSamples: make(map[string]interface{}),
		configs:       make(map[string]interface{}),
	}
}

// RegisterBlockFactory appends a block factory to the registry.
func (r *DefaultRegistry) RegisterBlockFactory(blockType int32, factory block.BlockFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blockFactories = append(r.blockFactories, BlockRegistration{BlockType: blockType, Factory: factory})
}

// RegisterGameSystem appends a gameloop.System to the registry.
func (r *DefaultRegistry) RegisterGameSystem(sys gameloop.System) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gameSystems = append(r.gameSystems, sys)
}

// RegisterPluginMeta appends plugin metadata to the registry.
func (r *DefaultRegistry) RegisterPluginMeta(meta PluginMeta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pluginMetas = append(r.pluginMetas, meta)
}

// RegisterHook appends a hook handler for a given hook type.
func (r *DefaultRegistry) RegisterHook(hook HookType, fn HookFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks[hook] = append(r.hooks[hook], fn)
}

// RegisterCommand appends a CLI command registration to the registry.
func (r *DefaultRegistry) RegisterCommand(name, description string, handler CommandFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, CommandRegistration{Name: name, Description: description, Handler: handler})
}

// RegisterPluginConfig registers a sample config struct for a plugin in the registry.
func (r *DefaultRegistry) RegisterPluginConfig(name string, sample interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.configSamples == nil {
		r.configSamples = make(map[string]interface{})
	}
	if r.configs == nil {
		r.configs = make(map[string]interface{})
	}
	r.configSamples[name] = sample
	r.configs[name] = sample
}

// LoadPluginConfig loads a plugin's YAML config from dir/name.yaml into the registry.
func (r *DefaultRegistry) LoadPluginConfig(name, dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sample, ok := r.configSamples[name]
	if !ok {
		return nil
	}
	t := reflect.TypeOf(sample)
	if t.Kind() != reflect.Ptr {
		return fmt.Errorf("config sample for %s must be a pointer to struct", name)
	}
	elem := t.Elem()
	newPtr := reflect.New(elem).Interface()
	path := filepath.Join(dir, name+".yaml")
	data, err := ioutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config file %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, newPtr); err != nil {
		return fmt.Errorf("parse config file %s: %w", path, err)
	}
	r.configs[name] = newPtr
	return nil
}

// PluginConfig returns the loaded config object for a plugin, or default sample.
func (r *DefaultRegistry) PluginConfig(name string) interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.configs[name]
}

// BlockFactories returns all registered block factories.
func (r *DefaultRegistry) BlockFactories() []BlockRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.blockFactories
}

// GameSystems returns all registered game systems.
func (r *DefaultRegistry) GameSystems() []gameloop.System {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gameSystems
}

// PluginMetas returns all registered plugin metadata.
func (r *DefaultRegistry) PluginMetas() []PluginMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pluginMetas
}

// Hooks returns all registered hook handlers for the given hook type.
func (r *DefaultRegistry) Hooks(hook HookType) []HookFunc {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.hooks[hook]
}

// Commands returns all registered CLI command registrations.
func (r *DefaultRegistry) Commands() []CommandRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.commands
}

// MarkCore marks the current registry state as the core, so plugin additions can be cleared later.
func (r *DefaultRegistry) MarkCore() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.coreBlockCount = len(r.blockFactories)
	r.coreSystemCount = len(r.gameSystems)
	r.coreCommandCount = len(r.commands)
	r.corePluginMetaCount = len(r.pluginMetas)
	// Snapshot hooks map
	r.coreHooks = make(map[HookType][]HookFunc, len(r.hooks))
	for k, v := range r.hooks {
		r.coreHooks[k] = append([]HookFunc{}, v...)
	}
}

// ClearPlugins removes all registrations added after the last core mark.
func (r *DefaultRegistry) ClearPlugins() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.coreBlockCount <= len(r.blockFactories) {
		r.blockFactories = r.blockFactories[:r.coreBlockCount]
	}
	if r.coreSystemCount <= len(r.gameSystems) {
		r.gameSystems = r.gameSystems[:r.coreSystemCount]
	}
	if r.coreCommandCount <= len(r.commands) {
		r.commands = r.commands[:r.coreCommandCount]
	}
	if r.corePluginMetaCount <= len(r.pluginMetas) {
		r.pluginMetas = r.pluginMetas[:r.corePluginMetaCount]
	}
	// Restore hooks to core snapshot
	r.hooks = make(map[HookType][]HookFunc, len(r.coreHooks))
	for k, v := range r.coreHooks {
		r.hooks[k] = append([]HookFunc{}, v...)
	}
}

// PluginAPIVersion defines the current plugin API version.
const PluginAPIVersion = "1"

// PluginManager handles loading of plugins from shared object files.
type PluginManager struct {
	// Dir is the directory where plugin .so files are located.
	Dir string
	// mu protects LoadPlugins from concurrent execution.
	mu sync.Mutex
}

// NewPluginManager creates a PluginManager for a given directory.
func NewPluginManager(dir string) *PluginManager {
	return &PluginManager{Dir: dir}
}

// Metrics for plugin loading
var (
	pluginLoadCount  = expvar.NewInt("plugins_loaded")
	pluginSkipCount  = expvar.NewInt("plugins_skipped")
	pluginErrorCount = expvar.NewInt("plugins_errors")
)

// LoadPlugins loads all plugins in pm.Dir and invokes their Register function.
func (pm *PluginManager) LoadPlugins(reg PluginRegistry) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	// List plugin files in directory
	files, err := ioutil.ReadDir(pm.Dir)
	if err != nil {
		pluginErrorCount.Add(1)
		return fmt.Errorf("cannot read plugin directory %s: %w", pm.Dir, err)
	}
	for _, f := range files {
		if filepath.Ext(f.Name()) != ".so" {
			continue
		}
		// Attempt to load plugin metadata from JSON/YAML, enforce API version.
		skip := false
		base := strings.TrimSuffix(f.Name(), ".so")
		for _, ext := range []string{".json", ".yaml", ".yml"} {
			metaPath := filepath.Join(pm.Dir, base+ext)
			data, err := ioutil.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var meta PluginMeta
			if ext == ".json" {
				if err := json.Unmarshal(data, &meta); err != nil {
					log.Printf("failed to parse plugin metadata %s: %v", metaPath, err)
					continue
				}
			} else {
				if err := yaml.Unmarshal(data, &meta); err != nil {
					log.Printf("failed to parse plugin metadata %s: %v", metaPath, err)
					continue
				}
			}
			// Version check
			if meta.Version != PluginAPIVersion {
				log.Printf("skipping plugin %s: version mismatch (got %s, expected %s)", meta.Name, meta.Version, PluginAPIVersion)
				skip = true
			} else {
				reg.RegisterPluginMeta(meta)
			}
			break
		}
		if skip {
			pluginSkipCount.Add(1)
			continue
		}
		pluginPath := filepath.Join(pm.Dir, f.Name())
		// Before plugin load hooks
		for _, h := range reg.Hooks(HookBeforePluginLoad) {
			h(pluginPath)
		}
		p, err := pluginpkg.Open(pluginPath)
		if err != nil {
			return fmt.Errorf("failed to open plugin %s: %w", pluginPath, err)
		}
		// Lookup Register symbol
		sym, err := p.Lookup("Register")
		if err != nil {
			// Skip plugins without Register function
			pluginErrorCount.Add(1)
			log.Printf("no Register symbol in %s: %v", pluginPath, err)
			continue
		}
		// Safely invoke plugin registration, catch panics
		func() {
			defer func() {
				if r := recover(); r != nil {
					pluginErrorCount.Add(1)
					log.Printf("panic in plugin %s Register: %v", pluginPath, r)
				}
			}()
			registerFunc, ok := sym.(func(PluginRegistry))
			if !ok {
				pluginErrorCount.Add(1)
				log.Printf("invalid Register signature in %s", pluginPath)
				return
			}
			// Invoke plugin registration (which may call RegisterPluginConfig)
			registerFunc(reg)
			// After registration, attempt to load plugin config
			if err := reg.LoadPluginConfig(base, pm.Dir); err != nil {
				pluginErrorCount.Add(1)
				log.Printf("failed to load config for plugin %s: %v", base, err)
			}
			pluginLoadCount.Add(1)
			// After plugin load hooks
			for _, h := range reg.Hooks(HookAfterPluginLoad) {
				h(pluginPath)
			}
		}()
	}
	return nil
}

// UnloadPlugins triggers unload hooks for all loaded plugins.
func (pm *PluginManager) UnloadPlugins(reg PluginRegistry) {
	// Before unload
	for _, meta := range reg.PluginMetas() {
		for _, h := range reg.Hooks(HookBeforePluginUnload) {
			h(meta)
		}
	}
	// After unload
	for _, meta := range reg.PluginMetas() {
		for _, h := range reg.Hooks(HookAfterPluginUnload) {
			h(meta)
		}
	}
}

// ReloadPlugins unloads existing plugins and reloads them.
func (pm *PluginManager) ReloadPlugins(reg PluginRegistry) error {
	// Trigger unload hooks for existing plugins
	pm.UnloadPlugins(reg)
	// Remove previous plugin registrations
	reg.ClearPlugins()
	// Load plugins anew
	return pm.LoadPlugins(reg)
}
