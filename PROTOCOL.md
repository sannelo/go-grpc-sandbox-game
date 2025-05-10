# gRPC Protocol – `proto/game/game.proto`

High-level description of the messages exchanged between server and client.

## Services

| RPC | Direction | Purpose |
|-----|-----------|---------|
| `JoinGame` | Unary | Auth + spawn position |
| `GameStream` | Bi-direct streaming | Real-time events (movement, world events, chat) |
| `GetChunks` | Server streaming | Bulk chunk transfer around player |
| `GenerateChunks` | Unary | Force pre-gen of chunks (admin) |

## Core messages (simplified)

### Client → Server

* **`PlayerMovement`** – absolute position & facing direction.  
* **`BlockAction`** – place/destroy/interact block at given coords.  
* **`ChatMessage`** – text chat, global or private.  
* **`Ping`** – RTT measurement.

### Server → Client

* **`WorldEvent`** – changes in the world (block placed, weather, time, entities).  
* **`PlayerUpdate`** – position/connection status of other players.  
* **`ChunkUpdate`** – delta patch of a chunk.  
* **`ChatBroadcast`** – chat messages.  
* **`Pong`** – ping response.

### WorldEvent payloads

| Type | When |
|------|------|
| `BLOCK_PLACED` / `BLOCK_DESTROYED` | Player or system edits block |
| `ENTITY_SPAWNED` / `ENTITY_DESTROYED` | NPC spawned or died |
| `WEATHER_CHANGED` | WeatherSystem toggled rain/storm |
| `TIME_CHANGED` | TimeSystem advanced time of day |

---

## Extending the protocol

1. Add fields to `proto/game/game.proto` using *reserved* tags to avoid breaking old clients.  
2. Regenerate Go code: `make proto`.  
3. Implement logic in corresponding systems or RPC handlers.  
4. Update client library (`pkg/protocol/game`).

---

**Tip:** For quick inspection use `grpcurl` with reflection (`-plaintext localhost:50051 list`).