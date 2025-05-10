package storage

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	util "github.com/annelo/go-grpc-server/internal/storage/util"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// BinaryStorage реализует интерфейс WorldStorage, используя многоуровневое бинарное хранилище
type BinaryStorage struct {
	basePath  string     // Базовый путь для файлов хранилища
	worldName string     // Название мира
	worldSeed int64      // Сид мира
	worldInfo *WorldInfo // Информация о мире

	// Менеджер регионов для долговременного хранения
	regionManager *RegionManager

	// Кеш дельт в памяти
	deltaCache map[string]*ChunkDelta
	cacheMutex sync.RWMutex

	// Список "грязных" (измененных) дельт, ожидающих сохранения
	dirtyDeltas map[string]time.Time

	// Канал для сохранения дельт в фоне
	saveQueue chan *game.ChunkPosition

	// Каналы для завершения работы
	stopChan chan struct{}
	wg       sync.WaitGroup

	// директория игроков
	playersPath string

	closeOnce sync.Once
}

// NewBinaryStorage создает новое бинарное хранилище
func NewBinaryStorage(basePath string, worldName string, seed int64) (*BinaryStorage, error) {
	// Создаем базовые директории
	err := os.MkdirAll(basePath, 0755)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать директорию хранилища: %w", err)
	}

	regionsPath := filepath.Join(basePath, "regions")
	err = os.MkdirAll(regionsPath, 0755)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать директорию регионов: %w", err)
	}

	playersPath := filepath.Join(basePath, "players")
	if err := os.MkdirAll(playersPath, 0755); err != nil {
		return nil, fmt.Errorf("не удалось создать директорию игроков: %w", err)
	}

	// Создаем экземпляр хранилища
	storage := &BinaryStorage{
		basePath:      basePath,
		worldName:     worldName,
		worldSeed:     seed,
		regionManager: NewRegionManager(regionsPath),
		deltaCache:    make(map[string]*ChunkDelta),
		dirtyDeltas:   make(map[string]time.Time),
		saveQueue:     make(chan *game.ChunkPosition, 100),
		stopChan:      make(chan struct{}),
		wg:            sync.WaitGroup{},
		playersPath:   playersPath,
	}

	// Загружаем или создаем информацию о мире
	info, err := storage.LoadWorld(context.Background())
	if err != nil {
		// Если информация не найдена, создаем новую
		now := time.Now().Unix()
		info = &WorldInfo{
			Name:       worldName,
			Seed:       seed,
			Version:    "binary-1.0.0",
			CreatedAt:  now,
			LastSaveAt: now,
			Properties: make(map[string]string),
		}

		// Сохраняем информацию о мире
		err = storage.SaveWorld(context.Background(), info)
		if err != nil {
			return nil, fmt.Errorf("ошибка при сохранении информации о мире: %w", err)
		}
	}

	storage.worldInfo = info

	// Запускаем фоновых рабочих
	storage.wg.Add(2)
	go storage.saveWorker()
	go storage.cleanupWorker()

	return storage, nil
}

// SaveChunk сохраняет чанк в хранилище
func (s *BinaryStorage) SaveChunk(ctx context.Context, chunk *game.Chunk) error {
	// Создаем или получаем дельту для чанка
	delta, err := s.GetOrCreateDelta(chunk.Position)
	if err != nil {
		return err
	}

	// Обрабатываем каждый блок в чанке
	changed := false
	for _, block := range chunk.Blocks {
		if delta.SetBlock(block) {
			changed = true
		}
	}

	if changed {
		// Помечаем дельту как измененную
		s.markDeltaAsDirty(chunk.Position)

		// Планируем сохранение
		s.queueForSaving(chunk.Position)
	}

	return nil
}

// LoadChunk загружает чанк из хранилища
func (s *BinaryStorage) LoadChunk(ctx context.Context, position *game.ChunkPosition) (*game.Chunk, error) {
	// Получаем дельту для чанка
	delta, err := s.GetDelta(position)
	if err != nil {
		return nil, ErrChunkNotFound{X: position.X, Y: position.Y}
	}

	// Создаем базовый чанк
	chunk := &game.Chunk{
		Position: position,
		Blocks:   make([]*game.Block, 0),
		Entities: make([]*game.Entity, 0),
	}

	// Применяем дельту к чанку
	for _, block := range delta.BlockChanges {
		if block.Type != 0 { // Пропускаем Air-блоки
			chunk.Blocks = append(chunk.Blocks, block)
		}
	}

	return chunk, nil
}

// DeleteChunk удаляет чанк из хранилища
func (s *BinaryStorage) DeleteChunk(ctx context.Context, position *game.ChunkPosition) error {
	key := util.ChunkKey(position)

	// Удаляем из кеша дельт
	s.cacheMutex.Lock()
	delete(s.deltaCache, key)
	delete(s.dirtyDeltas, key)
	s.cacheMutex.Unlock()

	// Создаем пустую дельту и сохраняем ее
	emptyDelta := NewChunkDelta(position)

	// Сохраняем пустую дельту в регион
	return s.regionManager.SaveChunkDelta(emptyDelta)
}

// ListChunks возвращает список всех сохраненных чанков
func (s *BinaryStorage) ListChunks(ctx context.Context) ([]*game.ChunkPosition, error) {
	// TODO: В текущей реализации мы не можем эффективно получить список всех чанков
	// из регионального хранилища. Для полной реализации потребуется обход всех регионов.
	// Это может быть неэффективно для больших миров.
	// Возвращаем список чанков из кеша как временное решение.

	s.cacheMutex.RLock()
	defer s.cacheMutex.RUnlock()

	chunks := make([]*game.ChunkPosition, 0, len(s.deltaCache))
	for _, delta := range s.deltaCache {
		chunks = append(chunks, delta.ChunkPos)
	}

	return chunks, nil
}

// SaveWorld сохраняет информацию о мире
func (s *BinaryStorage) SaveWorld(ctx context.Context, info *WorldInfo) error {
	// Обновляем время последнего сохранения
	info.LastSaveAt = time.Now().Unix()

	// Сохраняем информацию в JSON-файл
	infoPath := filepath.Join(s.basePath, "world_info.json")
	return saveJSONFile(infoPath, info)
}

// LoadWorld загружает информацию о мире
func (s *BinaryStorage) LoadWorld(ctx context.Context) (*WorldInfo, error) {
	infoPath := filepath.Join(s.basePath, "world_info.json")

	// Проверяем существование файла
	if _, err := os.Stat(infoPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("информация о мире не найдена")
	}

	// Загружаем информацию
	var info WorldInfo
	err := loadJSONFile(infoPath, &info)
	if err != nil {
		return nil, fmt.Errorf("ошибка при загрузке информации о мире: %w", err)
	}

	return &info, nil
}

// Close закрывает хранилище и освобождает ресурсы
func (s *BinaryStorage) Close() error {
	var retErr error
	s.closeOnce.Do(func() {
		// Сигнализируем рабочим остановиться
		close(s.stopChan)

		// Ждем завершения рабочих
		s.wg.Wait()

		// Сохраняем все измененные дельты
		s.saveAllDirtyDeltas()

		// Закрываем менеджер регионов
		retErr = s.regionManager.Close()
	})
	return retErr
}

// GetOrCreateDelta получает существующую дельту или создает новую
func (s *BinaryStorage) GetOrCreateDelta(pos *game.ChunkPosition) (*ChunkDelta, error) {
	key := util.ChunkKey(pos)

	// Сначала проверяем кеш
	s.cacheMutex.RLock()
	delta, exists := s.deltaCache[key]
	s.cacheMutex.RUnlock()

	if exists {
		delta.Touch() // Обновляем время доступа
		return delta, nil
	}

	// Пытаемся загрузить из регионального хранилища
	delta, err := s.regionManager.GetChunkDelta(pos)
	if err == nil {
		// Добавляем в кеш
		s.cacheMutex.Lock()
		s.deltaCache[key] = delta
		s.cacheMutex.Unlock()

		delta.Touch()
		return delta, nil
	}

	// Создаем новую дельту
	delta = NewChunkDelta(pos)

	// Добавляем в кеш
	s.cacheMutex.Lock()
	s.deltaCache[key] = delta
	s.cacheMutex.Unlock()

	return delta, nil
}

// GetDelta получает дельту чанка из кеша или хранилища
func (s *BinaryStorage) GetDelta(pos *game.ChunkPosition) (*ChunkDelta, error) {
	key := util.ChunkKey(pos)

	// Сначала проверяем кеш
	s.cacheMutex.RLock()
	delta, exists := s.deltaCache[key]
	s.cacheMutex.RUnlock()

	if exists {
		delta.Touch() // Обновляем время доступа
		return delta, nil
	}

	// Пытаемся загрузить из регионального хранилища
	delta, err := s.regionManager.GetChunkDelta(pos)
	if err != nil {
		return nil, err
	}

	// Добавляем в кеш
	s.cacheMutex.Lock()
	s.deltaCache[key] = delta
	s.cacheMutex.Unlock()

	delta.Touch()
	return delta, nil
}

// markDeltaAsDirty помечает дельту как измененную
func (s *BinaryStorage) markDeltaAsDirty(pos *game.ChunkPosition) {
	key := util.ChunkKey(pos)

	s.cacheMutex.Lock()
	s.dirtyDeltas[key] = time.Now()
	s.cacheMutex.Unlock()
}

// queueForSaving добавляет чанк в очередь на сохранение
func (s *BinaryStorage) queueForSaving(pos *game.ChunkPosition) {
	select {
	case s.saveQueue <- pos:
		// Успешно добавлено
	default:
		// Очередь заполнена, пропускаем (будет сохранено позже)
		log.Printf("Очередь сохранения заполнена, пропускаем чанк [%d, %d]", pos.X, pos.Y)
	}
}

// saveWorker обрабатывает очередь сохранения
func (s *BinaryStorage) saveWorker() {
	defer s.wg.Done()
	for {
		select {
		case pos := <-s.saveQueue:
			s.saveDelta(pos)
		case <-s.stopChan:
			return
		}
	}
}

// saveDelta сохраняет дельту в региональное хранилище
func (s *BinaryStorage) saveDelta(pos *game.ChunkPosition) {
	key := util.ChunkKey(pos)

	// Получаем дельту из кеша
	s.cacheMutex.RLock()
	delta, exists := s.deltaCache[key]
	s.cacheMutex.RUnlock()

	if !exists {
		return
	}

	// Сохраняем дельту в региональное хранилище
	err := s.regionManager.SaveChunkDelta(delta)
	if err != nil {
		log.Printf("Ошибка при сохранении дельты чанка [%d, %d]: %v", pos.X, pos.Y, err)
		return
	}

	// Удаляем из списка "грязных" дельт
	s.cacheMutex.Lock()
	delete(s.dirtyDeltas, key)
	s.cacheMutex.Unlock()

	// log.Printf("Сохранена дельта чанка [%d, %d]", pos.X, pos.Y)
}

// saveAllDirtyDeltas сохраняет все измененные дельты
func (s *BinaryStorage) saveAllDirtyDeltas() {
	// Создаем копию списка грязных дельт
	s.cacheMutex.RLock()
	dirtyKeys := make([]string, 0, len(s.dirtyDeltas))
	for k := range s.dirtyDeltas {
		dirtyKeys = append(dirtyKeys, k)
	}
	s.cacheMutex.RUnlock()

	// Сохраняем каждую дельту
	for _, key := range dirtyKeys {
		s.cacheMutex.RLock()
		delta, exists := s.deltaCache[key]
		s.cacheMutex.RUnlock()

		if !exists {
			continue
		}

		// Сохраняем дельту
		err := s.regionManager.SaveChunkDelta(delta)
		if err != nil {
			log.Printf("Ошибка при сохранении дельты чанка [%d, %d]: %v",
				delta.ChunkPos.X, delta.ChunkPos.Y, err)
			continue
		}

		// Удаляем из списка "грязных" дельт
		s.cacheMutex.Lock()
		delete(s.dirtyDeltas, key)
		s.cacheMutex.Unlock()
	}

	// Обновляем информацию о последнем сохранении
	s.worldInfo.LastSaveAt = time.Now().Unix()
	s.SaveWorld(context.Background(), s.worldInfo)
}

// cleanupWorker периодически очищает неиспользуемые дельты из кеша
func (s *BinaryStorage) cleanupWorker() {
	defer s.wg.Done()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.cleanupUnusedDeltas()
		case <-s.stopChan:
			return
		}
	}
}

// cleanupUnusedDeltas удаляет из кеша дельты, которые не использовались долгое время
func (s *BinaryStorage) cleanupUnusedDeltas() {
	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()

	// Если кеш не переполнен, не очищаем вовсе
	if len(s.deltaCache) <= MaxDeltaCacheSize {
		return
	}

	// Создаём срез дельт с информацией о последнем доступе и dirty-статусе
	type deltaWithTime struct {
		key   string
		at    time.Time
		dirty bool
	}

	deltas := make([]deltaWithTime, 0, len(s.deltaCache))
	for k, d := range s.deltaCache {
		_, isDirty := s.dirtyDeltas[k]
		deltas = append(deltas, deltaWithTime{k, d.AccessTime, isDirty})
	}

	// Сортируем по AccessTime (старые в начале), игнорируя dirty
	sort.Slice(deltas, func(i, j int) bool {
		// Грязные ставим в конец, чтобы не удалились
		if deltas[i].dirty != deltas[j].dirty {
			return !deltas[i].dirty
		}
		return deltas[i].at.Before(deltas[j].at)
	})

	removed := 0
	// Удаляем не более DeltaCacheCleanupBatch элементов, игнорируя dirty
	for _, item := range deltas {
		if removed >= DeltaCacheCleanupBatch {
			break
		}
		if item.dirty {
			continue
		}
		delete(s.deltaCache, item.key)
		removed++
	}

	if removed > 0 {
		log.Printf("Удалено %d устаревших дельт из кеша (batch)", removed)
	}
}

// Вспомогательные функции для работы с JSON
func saveJSONFile(path string, data interface{}) error {
	// Преобразуем данные в JSON
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	// Записываем данные в файл
	return os.WriteFile(path, jsonData, 0644)
}

func loadJSONFile(path string, data interface{}) error {
	// Читаем файл
	fileData, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Разбираем JSON
	return json.Unmarshal(fileData, data)
}

// SavePlayerState сохраняет состояние игрока в бинарном (gob) виде
func (s *BinaryStorage) SavePlayerState(ctx context.Context, state *PlayerState) error {
	if state == nil {
		return nil
	}
	path := filepath.Join(s.playersPath, fmt.Sprintf("player_%s.dat", state.ID))

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := gob.NewEncoder(f)
	return enc.Encode(state)
}

// LoadPlayerState загружает состояние игрока; ошибка, если файла нет
func (s *BinaryStorage) LoadPlayerState(ctx context.Context, id string) (*PlayerState, error) {
	path := filepath.Join(s.playersPath, fmt.Sprintf("player_%s.dat", id))
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	dec := gob.NewDecoder(file)
	var ps PlayerState
	if err := dec.Decode(&ps); err != nil {
		return nil, err
	}
	return &ps, nil
}
