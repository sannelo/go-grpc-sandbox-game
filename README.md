# Go gRPC Server with Plugin Architecture

This repository demonstrates a modular Go gRPC server with a flexible plugin system.
Plugins can register new block types, game loop systems, hooks, CLI commands, and load custom configurations.

---

## Table of Contents

1. [Getting Started](#getting-started)
2. [Plugin API Overview](#plugin-api-overview)
   - [PluginRegistry Interface](#pluginregistry-interface)
   - [DefaultRegistry](#defaultregistry)
3. [Writing a Plugin](#writing-a-plugin)
   - [Project Layout](#project-layout)
   - [`Register` Function](#register-function)
   - [Block Factories & Systems](#block-factories--systems)
   - [Hooks](#hooks)
   - [CLI Commands](#cli-commands)
   - [Configuration](#configuration)
4. [Admin CLI](#admin-cli)
5. [Running the Server](#running-the-server)
6. [Examples](#examples)

---

## Getting Started

1. Clone this repository:
   ```bash
   git clone https://github.com/annelo/go-grpc-server.git
   cd go-grpc-server
   ```

2. Build the server:
   ```bash
   go mod tidy
   go build ./cmd/server
   ```

3. Compile sample plugin:
   ```bash
   go build -buildmode=plugin -o plugins/sampleplugin/sampleplugin.so plugins/sampleplugin/plugin.go
   ```

4. Run the server:
   ```bash
   ./cmd/server/server --pprof=6060
   ```

---

## Plugin API Overview

### PluginRegistry Interface

```go
type PluginRegistry interface {
  RegisterBlockFactory(blockType int32, factory block.BlockFactory)
  RegisterGameSystem(sys gameloop.System)
  BlockFactories() []BlockRegistration
  GameSystems() []gameloop.System

  RegisterHook(hook HookType, fn HookFunc)
  Hooks(hook HookType) []HookFunc

  RegisterCommand(name, description string, handler CommandFunc)
  Commands() []CommandRegistration

  RegisterPluginConfig(name string, sample interface{})
  LoadPluginConfig(name, dir string) error
  PluginConfig(name string) interface{}
  
  RegisterPluginMeta(meta PluginMeta)
  PluginMetas() []PluginMeta

  MarkCore()
  ClearPlugins()
}
```

- **BlockRegistration**: pairs block type ID with factory function.
- **GameSystems**: systems run every game loop tick.
- **Hooks**: event callbacks (e.g. `HookBeforePlaceBlock`).
- **Commands**: custom CLI commands in admin REPL.
- **Config**: YAML-based plugin-specific configuration.
- **Meta**: load metadata (name/version).
- **Hot Reload**: `MarkCore`/`ClearPlugins` + `ReloadPlugins()`.

### DefaultRegistry

A thread-safe default implementation of `PluginRegistry`.

---

## Writing a Plugin

### Project Layout

```
plugins/
  myplugin/
    myplugin.go
    myplugin.yaml  # optional configuration
```

### `Register` Function

Your plugin should expose a `Register` function:

```go
package main

import (
  "github.com/annelo/go-grpc-server/internal/plugin"
)

func Register(reg plugin.PluginRegistry) {
  // register blocks, systems, hooks, commands
}
```

The server will load `plugins/*.so` and call `Register(reg)`.

### Block Factories & Systems

```go
reg.RegisterBlockFactory(100, func(x, y int32, pos *game.ChunkPosition, t int32) block.Block { ... })
reg.RegisterGameSystem(gameloop.NewWeatherSystem(seed))
```

### Hooks

```go
reg.RegisterHook(plugin.HookAfterPlaceBlock, func(args ...interface{}) {
  if event, ok := args[0].(*game.WorldEvent); ok { ... }
})
```

### CLI Commands

```go
reg.RegisterCommand("sayhello", "Print greeting", func(args []string) (string, error) {
  return "Hello, world!\n", nil
})
```

Invoke in admin REPL: `sayhello`.

### Configuration

1. Define a sample struct and register it:
   ```go
   type MyConfig struct {
     Message string `yaml:"message"`
     Count   int    `yaml:"count"`
   }
   reg.RegisterPluginConfig("myplugin", &MyConfig{})
   ```
2. Provide `plugins/myplugin/myplugin.yaml`:
   ```yaml
   message: "Greetings from plugin"
   count: 5
   ```
3. In CLI, view config:
   ```
   > config myplugin
   message: "Greetings from plugin"
   count: 5
   ```
4. Access in commands or hooks:
   ```go
   cfg := reg.PluginConfig("myplugin").(*MyConfig)
   ```

---

## Admin CLI

Available built-in commands:

- `help` - list commands
- `reload` - reload plugins on the fly
- `plugins` - list loaded plugins
- `config <plugin>` - show plugin config
- `stop` - stop server

---

## Running the Server

```bash
./cmd/server/server --pprof=6060
```

Then in the terminal REPL:
```
> help
> plugins
> config sampleplugin
> reload
> stop
```

---

## Examples

See `plugins/sampleplugin` for a full example that registers:

- A static block factory (type 100)
- A time system
- Hooks for block placement
- CLI command `sampleinfo`
- Configuration `sampleplugin.yaml`

Happy coding! 🎉
