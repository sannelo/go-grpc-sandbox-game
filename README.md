# Go gRPC Sandbox Game Server

![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go) ![gRPC](https://img.shields.io/badge/gRPC-Protocol-3362?logo=google-cloud) ![License](https://img.shields.io/github/license/sannelo/go-grpc-sandbox-game)

Procedurally-generated **2-D sandbox game** back-end written in Go.  
Focuses on *performance*, *extensibility* and *observability*:

* Binary region/chunk storage with compaction  
* Precise LRU memory management  
* Modular **GameLoop** (NPC, weather, time, active blocks)  
* gRPC protocol – easy to integrate with any client (Godot, Unity, web)  
* Built-in metrics & pprof endpoints  
* Benchmark client & terminal world viewer included

> **Status:** experimental / research project – not production-ready.

---

## Quick start

```bash
# run server
make server

# (in another terminal) run text client
make client

# run 100 simulated clients for 60 s
make bench N=100 DUR=60s
```

See `cmd/` for available binaries.

---

## Architecture

```
+-----------+     gRPC    +-----------+
|   Client  | <=========> |  Server   |
+-----------+            /+-----------+
                         |
                         | GameLoop (20 TPS)
                         |  ├── TimeSystem (day/night)
                         |  ├── WeatherSystem
                         |  └── … (NPC, Furnace, …)
                         |
                         | Managers
                         |  ├── PlayerManager
                         |  └── ChunkManager (proc-gen, storage)
                         |
                         | Storage (binary files)
```

* `WorldService` – main gRPC API.  
* `ChunkManager` – generates & caches chunks via noise functions.  
* `BinaryStorage` – region-file format, delta compaction.  
* `GameLoop` – pluggable systems executed every tick.

---

## Roadmap

- [x] GameLoop with Time & Weather systems  
- [ ] NPC AI prototype  
- [ ] Active blocks (furnace)  
- [ ] Node-server clustering  
- [ ] Integration/E2E tests

---

## License

MIT © 2025 sannelo