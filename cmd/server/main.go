package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof" // pprof endpoints
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/annelo/go-grpc-server/internal/service"
	"github.com/annelo/go-grpc-server/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var (
	port      = flag.Int("port", 50051, "Порт для gRPC сервера")
	worldPath = flag.String("world", "/tmp/world", "Путь для хранения данных мира")
	worldName = flag.String("name", "default", "Название игрового мира")
	seed      = flag.Int64("seed", 0, "Сид для генерации мира (0 = случайный)")
	noStorage = flag.Bool("no-storage", false, "Запуск без хранилища данных")
	pprofPort = flag.Int("pprof", 6060, "Порт для HTTP pprof/expvar, 0 = отключить")
)

func main() {
	// Парсим флаги командной строки
	flag.Parse()

	// Если сид не указан, генерируем случайный
	if *seed == 0 {
		*seed = time.Now().UnixNano()
	}

	// Создаем TCP-слушатель
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("Не удалось создать слушателя: %v", err)
	}

	// Создаем новый gRPC сервер
	grpcServer := grpc.NewServer()

	// Создаем контекст для управления сервисными задачами
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Инициализируем хранилище данных, если оно включено
	var worldService *service.WorldService

	if *noStorage {
		log.Printf("Запуск в режиме без хранилища")
		worldService = service.NewWorldService()
	} else {
		// Проверяем наличие директории и права доступа
		_, err := os.Stat(*worldPath)
		if os.IsNotExist(err) {
			// Пытаемся создать директорию
			err = os.MkdirAll(*worldPath, 0755)
			if err != nil {
				log.Printf("Невозможно создать директорию для хранилища %s: %v", *worldPath, err)
				log.Printf("Продолжаем без хранилища...")
				worldService = service.NewWorldService()
				worldService.RegisterServer(grpcServer)
				worldService.Start(ctx)
				goto StartServer
			}
		}

		// Проверяем права на запись
		testFile := filepath.Join(*worldPath, ".write_test")
		err = os.WriteFile(testFile, []byte("test"), 0644)
		if err != nil {
			log.Printf("Нет прав на запись в директорию хранилища %s: %v", *worldPath, err)
			log.Printf("Продолжаем без хранилища...")
			worldService = service.NewWorldService()
			worldService.RegisterServer(grpcServer)
			worldService.Start(ctx)
			goto StartServer
		}
		os.Remove(testFile) // Удаляем тестовый файл

		// Инициализируем хранилище
		worldStorage, err := storage.NewBinaryStorage(*worldPath, *worldName, *seed)
		if err != nil {
			log.Printf("Ошибка при инициализации хранилища: %v", err)
			log.Printf("Продолжаем без хранилища...")

			worldService = service.NewWorldService()
		} else {
			log.Printf("Бинарное хранилище мира инициализировано в %s", *worldPath)
			worldService = service.NewWorldServiceWithStorage(worldStorage)
		}
	}

	// Запускаем http-pprof/expvar, если включено
	if *pprofPort != 0 {
		go func() {
			addr := fmt.Sprintf(":%d", *pprofPort)
			log.Printf("Запуск pprof/expvar на %s", addr)
			if err := http.ListenAndServe(addr, nil); err != nil {
				log.Printf("pprof server error: %v", err)
			}
		}()
	}

	// Регистрируем и запускаем сервис
	worldService.RegisterServer(grpcServer)
	worldService.Start(ctx)

StartServer:
	// Включаем reflection для инструментов вроде grpcurl
	reflection.Register(grpcServer)

	// Обрабатываем сигналы для корректного завершения
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalChan
		log.Println("Получен сигнал завершения, останавливаем сервер...")
		cancel() // Отменяем контекст для всех сервисных задач
		worldService.Stop()
		grpcServer.GracefulStop()
	}()

	// Запускаем сервер
	log.Printf("Игровой сервер запущен на порту %d", *port)
	log.Printf("Используется сид мира: %d", *seed)

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Ошибка запуска сервера: %v", err)
	}
}
