package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	util "github.com/annelo/go-grpc-server/internal/storage/util"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// RegionFile представляет файл региона, содержащий множество чанков
type RegionFile struct {
	filename       string
	file           *os.File
	headerSize     int
	indexTableSize int
	chunkCount     int
	mutex          sync.RWMutex

	// Кеш индексов для быстрого доступа
	chunkIndex map[string]chunkIndexEntry
}

// Запись в индексной таблице
type chunkIndexEntry struct {
	X           int32
	Y           int32
	Offset      uint32
	Size        uint16
	LastModTime uint16
}

// NewRegionFile создает новый файл региона или открывает существующий
func NewRegionFile(path string, regionX, regionY int32) (*RegionFile, error) {
	filename := fmt.Sprintf("bchunk_%d_%d.dat", regionX, regionY)
	fullPath := filepath.Join(path, filename)

	// Проверяем, существует ли файл
	exists := false
	if _, err := os.Stat(fullPath); err == nil {
		exists = true
	}

	// Открываем файл
	file, err := os.OpenFile(fullPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}

	region := &RegionFile{
		filename:       fullPath,
		file:           file,
		headerSize:     256,
		indexTableSize: 16 * 256, // 16 байт на запись × 256 чанков
		chunkCount:     256,
		chunkIndex:     make(map[string]chunkIndexEntry),
	}

	// Если файл новый, инициализируем его
	if !exists {
		if err := region.initializeFile(regionX, regionY); err != nil {
			file.Close()
			return nil, err
		}
	} else {
		// Загружаем индексную таблицу в память
		if err := region.loadIndexTable(); err != nil {
			file.Close()
			return nil, err
		}
	}

	return region, nil
}

// Инициализация нового файла
func (r *RegionFile) initializeFile(regionX, regionY int32) error {
	// Создаем заголовок
	header := make([]byte, r.headerSize)

	// Сигнатура "BREG"
	copy(header[0:4], []byte("BREG"))

	// Версия формата (1)
	binary.LittleEndian.PutUint32(header[4:8], 1)

	// Размер региона (256 чанков)
	binary.LittleEndian.PutUint32(header[8:12], 256)

	// Время создания
	binary.LittleEndian.PutUint64(header[12:20], uint64(time.Now().Unix()))

	// Последнее обновление (то же, что и время создания)
	binary.LittleEndian.PutUint64(header[20:28], uint64(time.Now().Unix()))

	// Записываем заголовок
	if _, err := r.file.Write(header); err != nil {
		return err
	}

	// Создаем пустую индексную таблицу
	indexTable := make([]byte, r.indexTableSize)

	// Заполняем индексную таблицу нулями
	localX, localY := 0, 0
	for i := 0; i < 256; i++ {
		// Вычисляем локальные координаты чанка в регионе
		localX = i % 16
		localY = i / 16

		offset := i * 16

		// Задаем координаты чанка
		binary.LittleEndian.PutUint32(indexTable[offset:offset+4], uint32(regionX*16+int32(localX)))
		binary.LittleEndian.PutUint32(indexTable[offset+4:offset+8], uint32(regionY*16+int32(localY)))

		// Нулевое смещение и размер означают, что чанк еще не сохранен
		binary.LittleEndian.PutUint32(indexTable[offset+8:offset+12], 0)
		binary.LittleEndian.PutUint16(indexTable[offset+12:offset+14], 0)
		binary.LittleEndian.PutUint16(indexTable[offset+14:offset+16], 0)
	}

	// Записываем индексную таблицу
	if _, err := r.file.Write(indexTable); err != nil {
		return err
	}

	return r.file.Sync()
}

// Загрузка индексной таблицы в память
func (r *RegionFile) loadIndexTable() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Перемещаем указатель в начало индексной таблицы
	if _, err := r.file.Seek(int64(r.headerSize), 0); err != nil {
		return err
	}

	// Читаем индексную таблицу
	indexTable := make([]byte, r.indexTableSize)
	if _, err := r.file.Read(indexTable); err != nil {
		return err
	}

	// Разбираем индексную таблицу
	for i := 0; i < 256; i++ {
		offset := i * 16

		x := int32(binary.LittleEndian.Uint32(indexTable[offset : offset+4]))
		y := int32(binary.LittleEndian.Uint32(indexTable[offset+4 : offset+8]))
		dataOffset := binary.LittleEndian.Uint32(indexTable[offset+8 : offset+12])
		size := binary.LittleEndian.Uint16(indexTable[offset+12 : offset+14])
		lastMod := binary.LittleEndian.Uint16(indexTable[offset+14 : offset+16])

		// Сохраняем только существующие чанки (с ненулевым размером)
		if size > 0 {
			key := util.ChunkKey(&game.ChunkPosition{X: x, Y: y})
			r.chunkIndex[key] = chunkIndexEntry{
				X:           x,
				Y:           y,
				Offset:      dataOffset,
				Size:        size,
				LastModTime: lastMod,
			}
		}
	}

	return nil
}

// GetChunk получает чанк из файла
func (r *RegionFile) GetChunk(pos *game.ChunkPosition) (*ChunkDelta, error) {
	r.mutex.RLock()
	key := util.ChunkKey(pos)
	entry, exists := r.chunkIndex[key]
	r.mutex.RUnlock()

	if !exists || entry.Size == 0 {
		return nil, fmt.Errorf("чанк не найден")
	}

	// Перемещаем указатель на начало данных чанка
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if _, err := r.file.Seek(int64(entry.Offset), 0); err != nil {
		return nil, err
	}

	// Читаем данные чанка
	data := make([]byte, entry.Size)
	if _, err := r.file.Read(data); err != nil {
		return nil, err
	}

	// Десериализуем данные в ChunkDelta
	chunk, err := r.deserializeChunk(data, pos)
	if err != nil {
		return nil, err
	}

	return chunk, nil
}

// SaveChunk сохраняет чанк в файл
func (r *RegionFile) SaveChunk(chunk *ChunkDelta) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Сериализуем чанк
	data, err := r.serializeChunk(chunk)
	if err != nil {
		return err
	}

	// Определяем позицию для записи
	var offset uint32
	key := util.ChunkKey(chunk.ChunkPos)
	entry, exists := r.chunkIndex[key]

	// Если чанк существует и новые данные помещаются в старое место
	if exists && entry.Offset > 0 && uint16(len(data)) <= entry.Size {
		offset = entry.Offset
	} else {
		// Иначе записываем в конец файла
		fileInfo, err := r.file.Stat()
		if err != nil {
			return err
		}
		offset = uint32(fileInfo.Size())
	}

	// Перемещаем указатель на позицию записи
	if _, err := r.file.Seek(int64(offset), 0); err != nil {
		return err
	}

	// Записываем данные чанка
	if _, err := r.file.Write(data); err != nil {
		return err
	}

	// Обновляем индексную таблицу
	indexEntry := chunkIndexEntry{
		X:           chunk.ChunkPos.X,
		Y:           chunk.ChunkPos.Y,
		Offset:      offset,
		Size:        uint16(len(data)),
		LastModTime: uint16(time.Now().Unix() % 65536), // Упрощенное относительное время
	}

	// Сохраняем в кеш
	r.chunkIndex[key] = indexEntry

	// Обновляем индексную запись в файле
	idxOffset := r.findIndexOffset(chunk.ChunkPos)
	if idxOffset >= 0 {
		if _, err := r.file.Seek(int64(r.headerSize+idxOffset), 0); err != nil {
			return err
		}

		// Записываем индексную запись
		indexBytes := make([]byte, 16)
		binary.LittleEndian.PutUint32(indexBytes[0:4], uint32(chunk.ChunkPos.X))
		binary.LittleEndian.PutUint32(indexBytes[4:8], uint32(chunk.ChunkPos.Y))
		binary.LittleEndian.PutUint32(indexBytes[8:12], offset)
		binary.LittleEndian.PutUint16(indexBytes[12:14], uint16(len(data)))
		binary.LittleEndian.PutUint16(indexBytes[14:16], indexEntry.LastModTime)

		if _, err := r.file.Write(indexBytes); err != nil {
			return err
		}
	}

	// Синхронизируем файл с диском
	return r.file.Sync()
}

// Поиск позиции в индексной таблице для чанка
func (r *RegionFile) findIndexOffset(pos *game.ChunkPosition) int {
	// Получаем локальные координаты в регионе
	localX := pos.X % 16
	if localX < 0 {
		localX += 16
	}

	localY := pos.Y % 16
	if localY < 0 {
		localY += 16
	}

	// Вычисляем индекс в таблице
	idx := localY*16 + localX

	// Проверяем, что индекс в допустимом диапазоне
	if idx < 0 || idx >= 256 {
		return -1
	}

	return int(idx * 16)
}

// Сериализация чанка в бинарный формат
func (r *RegionFile) serializeChunk(chunk *ChunkDelta) ([]byte, error) {
	// Буфер для данных
	buf := new(bytes.Buffer)

	// Записываем заголовок чанка (8 байт)
	binary.Write(buf, binary.LittleEndian, uint16(1))                       // Версия
	binary.Write(buf, binary.LittleEndian, uint16(0))                       // Флаги
	binary.Write(buf, binary.LittleEndian, uint32(len(chunk.BlockChanges))) // Счетчик изменений

	// Записываем количество блоков
	binary.Write(buf, binary.LittleEndian, uint16(len(chunk.BlockChanges)))

	// Записываем блоки
	for _, block := range chunk.BlockChanges {
		// Локальные координаты должны быть в диапазоне 0-255
		if block.X < 0 || block.X > 255 || block.Y < 0 || block.Y > 255 {
			continue
		}

		binary.Write(buf, binary.LittleEndian, uint8(block.X))
		binary.Write(buf, binary.LittleEndian, uint8(block.Y))
		binary.Write(buf, binary.LittleEndian, uint8(block.Type))

		// Если у блока есть свойства, сохраняем их
		if block.Properties != nil && len(block.Properties) > 0 {
			// Количество свойств
			binary.Write(buf, binary.LittleEndian, uint8(len(block.Properties)))

			// Записываем свойства
			for k, v := range block.Properties {
				// Длина ключа
				keyBytes := []byte(k)
				binary.Write(buf, binary.LittleEndian, uint8(len(keyBytes)))
				buf.Write(keyBytes)

				// Длина значения
				valBytes := []byte(v)
				binary.Write(buf, binary.LittleEndian, uint8(len(valBytes)))
				buf.Write(valBytes)
			}
		} else {
			// Нет свойств
			binary.Write(buf, binary.LittleEndian, uint8(0))
		}
	}

	return buf.Bytes(), nil
}

// Десериализация чанка из бинарного формата
func (r *RegionFile) deserializeChunk(data []byte, pos *game.ChunkPosition) (*ChunkDelta, error) {
	buf := bytes.NewReader(data)

	// Читаем заголовок
	var version, flags uint16
	var changeCount uint32

	if err := binary.Read(buf, binary.LittleEndian, &version); err != nil {
		return nil, err
	}

	if err := binary.Read(buf, binary.LittleEndian, &flags); err != nil {
		return nil, err
	}

	if err := binary.Read(buf, binary.LittleEndian, &changeCount); err != nil {
		return nil, err
	}

	// Создаем новую дельту
	delta := &ChunkDelta{
		ChunkPos:     pos,
		BaseVersion:  int64(version),
		BlockChanges: make(map[uint16]*game.Block),
		CreatedAt:    time.Now(),
		LastModified: time.Now(),
		AccessTime:   time.Now(),
	}

	// Читаем количество блоков
	var blockCount uint16
	if err := binary.Read(buf, binary.LittleEndian, &blockCount); err != nil {
		return nil, err
	}

	// Читаем блоки
	for i := 0; i < int(blockCount); i++ {
		var x, y, blockType uint8

		if err := binary.Read(buf, binary.LittleEndian, &x); err != nil {
			return nil, err
		}

		if err := binary.Read(buf, binary.LittleEndian, &y); err != nil {
			return nil, err
		}

		if err := binary.Read(buf, binary.LittleEndian, &blockType); err != nil {
			return nil, err
		}

		// Создаем блок
		block := &game.Block{
			X:    int32(x),
			Y:    int32(y),
			Type: int32(blockType),
		}

		// Читаем свойства блока
		var propCount uint8
		if err := binary.Read(buf, binary.LittleEndian, &propCount); err != nil {
			return nil, err
		}

		if propCount > 0 {
			block.Properties = make(map[string]string, propCount)

			for j := 0; j < int(propCount); j++ {
				// Читаем ключ
				var keyLen uint8
				if err := binary.Read(buf, binary.LittleEndian, &keyLen); err != nil {
					return nil, err
				}

				keyBytes := make([]byte, keyLen)
				if _, err := buf.Read(keyBytes); err != nil {
					return nil, err
				}

				// Читаем значение
				var valLen uint8
				if err := binary.Read(buf, binary.LittleEndian, &valLen); err != nil {
					return nil, err
				}

				valBytes := make([]byte, valLen)
				if _, err := buf.Read(valBytes); err != nil {
					return nil, err
				}

				// Добавляем свойство
				block.Properties[string(keyBytes)] = string(valBytes)
			}
		}

		// Добавляем блок в дельту
		key := util.BlockKey(block.X, block.Y)
		delta.BlockChanges[key] = block
	}

	return delta, nil
}

// Close закрывает файл региона
func (r *RegionFile) Close() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	return r.file.Close()
}

// Sync принудительно сбрасывает буферы файла на диск.
func (r *RegionFile) Sync() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.file.Sync()
}

// NeedsCompaction определяет, необходимо ли выполнять компактацию файла региона.
// Критерием является превышение фактического размера файла над объёмом «живых»
// данных (заголовок + индекс + сохранённые дельты) более чем на
// RegionCompactionGrowFactor.
func (r *RegionFile) NeedsCompaction() bool {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	fileInfo, err := r.file.Stat()
	if err != nil {
		return false // безопасно: если не удалось получить размер, не компактим
	}

	// Подсчитываем используемый объём (header + index + суммарный размер дельт)
	usedSize := uint32(r.headerSize + r.indexTableSize)
	for _, entry := range r.chunkIndex {
		usedSize += uint32(entry.Size)
	}

	// Сравниваем с фактическим размером файла
	return float64(fileInfo.Size()) > float64(usedSize)*RegionCompactionGrowFactor
}

// Compact выполняет копирование всех «живых» дельт в новый временный файл,
// после чего атомарно заменяет им старый. Метод блокирует RegionFile на время
// операции, поэтому вызывается редко.
func (r *RegionFile) Compact() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Проверяем, действительно ли есть смысл компактации
	if !r.NeedsCompaction() {
		return nil
	}

	// Определяем координаты региона из имени файла
	base := filepath.Base(r.filename)
	var regionX, regionY int32
	if _, err := fmt.Sscanf(base, "bchunk_%d_%d.dat", &regionX, &regionY); err != nil {
		return fmt.Errorf("не удалось разобрать координаты региона из имени %s: %w", base, err)
	}

	// Путь к новому временному файлу
	tmpPath := r.filename + ".tmp"
	// На всякий случай удаляем существующий tmp
	_ = os.Remove(tmpPath)

	tmpFile, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("создание tmp-файла для компактации: %w", err)
	}

	// Создаём вспомогательную RegionFile для tmp
	tmpRegion := &RegionFile{
		filename:       tmpPath,
		file:           tmpFile,
		headerSize:     r.headerSize,
		indexTableSize: r.indexTableSize,
		chunkCount:     r.chunkCount,
		chunkIndex:     make(map[string]chunkIndexEntry),
	}

	// Инициализируем заголовок и пустую индекс-таблицу
	if err := tmpRegion.initializeFile(regionX, regionY); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("инициализация tmp-файла: %w", err)
	}

	// Копируем все существующие дельты
	for key, entry := range r.chunkIndex {
		// Читаем данные из старого файла
		if _, err := r.file.Seek(int64(entry.Offset), 0); err != nil {
			return err
		}

		data := make([]byte, entry.Size)
		if _, err := r.file.Read(data); err != nil {
			return err
		}

		// Десериализуем, чтобы сохранить через API tmpRegion (обновятся индексы)
		pos := &game.ChunkPosition{X: entry.X, Y: entry.Y}
		delta, err := r.deserializeChunk(data, pos)
		if err != nil {
			return err
		}

		if err := tmpRegion.SaveChunk(delta); err != nil {
			return err
		}

		// Сохраняем в кеше tmpRegion.chunkIndex уже происходит в SaveChunk
		// Копируем entry.LastModTime
		if te, ok := tmpRegion.chunkIndex[key]; ok {
			te.LastModTime = entry.LastModTime
			tmpRegion.chunkIndex[key] = te
		}
	}

	// Синхронизируем и закрываем tmp
	if err := tmpRegion.file.Sync(); err != nil {
		tmpRegion.file.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpRegion.file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	// Закрываем текущий файл перед заменой
	if err := r.file.Close(); err != nil {
		return err
	}

	// Атомарно заменяем файл
	if err := os.Rename(tmpPath, r.filename); err != nil {
		return err
	}

	// Переоткрываем файл
	newFile, err := os.OpenFile(r.filename, os.O_RDWR, 0644)
	if err != nil {
		return err
	}

	// Обновляем состояние текущего RegionFile
	r.file = newFile
	r.chunkIndex = tmpRegion.chunkIndex

	return nil
}
