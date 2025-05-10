package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
	"google.golang.org/grpc"
)

var (
	serverAddr   = flag.String("addr", "localhost:50051", "gRPC адрес сервера")
	clientsCount = flag.Int("n", 100, "Количество эмулируемых клиентов")
	duration     = flag.Duration("duration", 30*time.Second, "Длительность теста")
)

func main() {
	flag.Parse()
	log.Printf("Запускаем bClient: %d клиентов на %s в течение %s", *clientsCount, *serverAddr, *duration)

	var wg sync.WaitGroup
	stopCtx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	for i := 0; i < *clientsCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			runClient(stopCtx, id)
		}(i)
	}

	wg.Wait()
	log.Printf("bClient завершил работу")
}

func runClient(ctx context.Context, id int) {
	conn, err := grpc.Dial(*serverAddr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Printf("[client %d] dial error: %v", id, err)
		return
	}
	defer conn.Close()

	client := game.NewWorldServiceClient(conn)
	// Join
	joinResp, err := client.JoinGame(ctx, &game.JoinRequest{PlayerName: fmt.Sprintf("bot-%d", id)})
	if err != nil || !joinResp.Success {
		log.Printf("[client %d] join error: %v", id, err)
		return
	}
	playerID := joinResp.PlayerId
	playerPos := joinResp.SpawnPosition

	// Open stream
	stream, err := client.GameStream(ctx)
	if err != nil {
		log.Printf("[client %d] stream error: %v", id, err)
		return
	}

	// отправляем первое сообщение для идентификации
	if err := stream.Send(&game.ClientMessage{PlayerId: playerID}); err != nil {
		log.Printf("[client %d] send id error: %v", id, err)
		return
	}

	randSrc := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// случайное движение
			dx := randSrc.Intn(3) - 1
			dy := randSrc.Intn(3) - 1
			for range make([]struct{}, randSrc.Intn(100) + 1) {
				newPos := &game.Position{X: playerPos.X + float32(dx), Y: playerPos.Y + float32(dy)}
				move := &game.PlayerMovement{NewPosition: newPos}
				stream.Send(&game.ClientMessage{PlayerId: playerID, Payload: &game.ClientMessage_Movement{Movement: move}})
				playerPos = newPos
			}

			// случайное действие с блоком 10% шанс
			if randSrc.Intn(10) == 0 {
				actionType := game.BlockAction_PLACE
				if randSrc.Intn(2) == 0 {
					actionType = game.BlockAction_DESTROY
				}
				blkAction := &game.BlockAction{
					Action:    actionType,
					Position:  &game.Position{X: playerPos.X, Y: playerPos.Y},
					BlockType: int32(randSrc.Intn(5) + 1),
				}
				stream.Send(&game.ClientMessage{PlayerId: playerID, Payload: &game.ClientMessage_BlockAction{BlockAction: blkAction}})
			}
		}
	}
}
