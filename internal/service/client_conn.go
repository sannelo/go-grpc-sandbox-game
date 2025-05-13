package service

import "github.com/annelo/go-grpc-server/pkg/protocol/game"

// clientConn represents a client connection with priority queues for messaging.
type clientConn struct {
	stream      game.WorldService_GameStreamServer
	highQueue   chan *game.ServerMessage // For high-priority world events
	normalQueue chan *game.ServerMessage // For normal events
}

// send enqueues a message into the appropriate queue. If block is true, it blocks until the message is enqueued; otherwise it drops on overflow.
func (c *clientConn) send(msg *game.ServerMessage, block bool) {
	var q chan *game.ServerMessage
	if evMsg, ok := msg.Payload.(*game.ServerMessage_WorldEvent); ok {
		switch evMsg.WorldEvent.Type {
		case game.WorldEvent_WEATHER_CHANGED, game.WorldEvent_TIME_CHANGED, game.WorldEvent_SERVER_SHUTDOWN:
			q = c.highQueue
		default:
			q = c.normalQueue
		}
	} else {
		q = c.normalQueue
	}
	if block {
		q <- msg
	} else {
		select {
		case q <- msg:
		default:
		}
	}
}
