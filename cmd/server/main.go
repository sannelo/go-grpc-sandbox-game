package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/annelo/go-grpc-server/internal/block"
	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/gameloop"
	"github.com/annelo/go-grpc-server/internal/plugin"
	"github.com/annelo/go-grpc-server/internal/service"
	"github.com/annelo/go-grpc-server/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	yaml "gopkg.in/yaml.v3"
)

var (
	port      = flag.Int("port", 50051, "Порт для gRPC сервера")
	worldPath = flag.String("world", "/tmp/world", "Путь для хранения данных мира")
	worldName = flag.String("name", "default", "Название игрового мира")
	seed      = flag.Int64("seed", 0, "Сид для генерации мира (0 = случайный)")
	noStorage = flag.Bool("no-storage", false, "Запуск без хранилища данных")
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

	// 1) Инициализируем реестр плагинов
	reg := plugin.NewDefaultRegistry()
	// 2) Регистрируем core game-системы
	reg.RegisterGameSystem(gameloop.NewTimeSystem())
	reg.RegisterGameSystem(gameloop.NewWeatherSystem(*seed))
	reg.RegisterGameSystem(gameloop.NewBlockSystem())
	// 2) Регистрируем core статические блоки
	reg.RegisterBlockFactory(chunkmanager.BlockTypeAir, block.NewStaticBlock)
	reg.RegisterBlockFactory(chunkmanager.BlockTypeGrass, block.NewStaticBlock)
	reg.RegisterBlockFactory(chunkmanager.BlockTypeDirt, block.NewStaticBlock)
	reg.RegisterBlockFactory(chunkmanager.BlockTypeStone, block.NewStaticBlock)
	reg.RegisterBlockFactory(chunkmanager.BlockTypeWater, block.NewStaticBlock)
	reg.RegisterBlockFactory(chunkmanager.BlockTypeSand, block.NewStaticBlock)
	reg.RegisterBlockFactory(chunkmanager.BlockTypeWood, block.NewStaticBlock)
	reg.RegisterBlockFactory(chunkmanager.BlockTypeLeaves, block.NewStaticBlock)
	reg.RegisterBlockFactory(chunkmanager.BlockTypeSnow, block.NewStaticBlock)
	reg.RegisterBlockFactory(chunkmanager.BlockTypeTallGrass, block.NewStaticBlock)
	reg.RegisterBlockFactory(chunkmanager.BlockTypeFlower, block.NewStaticBlock)
	// 3) Регистрируем core динамические блоки
	reg.RegisterBlockFactory(block.BlockTypeFire, block.NewFireBlock)
	reg.RegisterBlockFactory(block.BlockTypeCrop, block.NewCropBlock)
	// 4) Регистрируем core интерактивные блоки
	reg.RegisterBlockFactory(block.BlockTypeDoor, block.NewDoorBlock)
	reg.RegisterBlockFactory(block.BlockTypeChest, block.NewChestBlock)
	// 3) Обозначаем границу core-регистраций и загружаем плагины
	pm := plugin.NewPluginManager("./plugins")
	reg.MarkCore()
	if err := pm.LoadPlugins(reg); err != nil {
		log.Printf("Ошибка при загрузке плагинов: %v", err)
	}

	// Инициализируем хранилище данных, если оно включено
	var worldService *service.WorldService

	if *noStorage {
		log.Printf("Запуск в режиме без хранилища")
		worldService = service.NewWorldService(reg)
	} else {
		// Проверяем наличие директории и права доступа
		_, err := os.Stat(*worldPath)
		if os.IsNotExist(err) {
			// Пытаемся создать директорию
			err = os.MkdirAll(*worldPath, 0755)
			if err != nil {
				log.Printf("Невозможно создать директорию для хранилища %s: %v", *worldPath, err)
				log.Printf("Продолжаем без хранилища...")
				worldService = service.NewWorldService(reg)
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
			worldService = service.NewWorldService(reg)
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

			worldService = service.NewWorldService(reg)
		} else {
			log.Printf("Бинарное хранилище мира инициализировано в %s", *worldPath)
			worldService = service.NewWorldServiceWithRegistry(reg, worldStorage)
		}
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

	// CLI для администратора: регистрируем встроенные команды
	reg.RegisterCommand("reload", "Reload plugins", func(args []string) (string, error) {
		if err := pm.ReloadPlugins(reg); err != nil {
			return "", err
		}
		return "Plugins reloaded successfully\n", nil
	})
	reg.RegisterCommand("stop", "Stop server", func(args []string) (string, error) {
		cancel()
		grpcServer.GracefulStop()
		return "Server stopping\n", nil
	})
	reg.RegisterCommand("help", "List commands", func(args []string) (string, error) {
		var sb strings.Builder
		for _, cmd := range reg.Commands() {
			sb.WriteString(fmt.Sprintf("%s - %s\n", cmd.Name, cmd.Description))
		}
		return sb.String(), nil
	})
	// List loaded plugins
	reg.RegisterCommand("plugins", "List loaded plugins", func(args []string) (string, error) {
		var sb strings.Builder
		for _, meta := range reg.PluginMetas() {
			sb.WriteString(fmt.Sprintf("%s v%s by %s: %s\n", meta.Name, meta.Version, meta.Author, meta.Description))
		}
		return sb.String(), nil
	})
	// Show plugin config
	reg.RegisterCommand("config", "Show plugin config: config <pluginName>", func(args []string) (string, error) {
		if len(args) < 1 {
			return "Usage: config <pluginName>\n", nil
		}
		name := args[0]
		cfg := reg.PluginConfig(name)
		if cfg == nil {
			return fmt.Sprintf("No config for plugin %s\n", name), nil
		}
		data, err := yaml.Marshal(cfg)
		if err != nil {
			return "", err
		}
		return string(data), nil
	})
	// CLI для администратора: REPL для команд
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Print("> ")
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			input := strings.TrimSpace(line)
			parts := strings.Fields(input)
			if len(parts) == 0 {
				continue
			}
			name := parts[0]
			args := parts[1:]
			found := false
			for _, cmdReg := range reg.Commands() {
				if cmdReg.Name == name {
					found = true
					out, err := cmdReg.Handler(args)
					if err != nil {
						fmt.Printf("Error: %v\n", err)
					} else {
						fmt.Print(out)
					}
					break
				}
			}
			if !found {
				fmt.Printf("Неизвестная команда: %s\n", name)
			}
		}
	}()

	// Запускаем сервер
	log.Printf("Игровой сервер запущен на порту %d", *port)
	log.Printf("Используется сид мира: %d", *seed)

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Ошибка запуска сервера: %v", err)
	}
}
