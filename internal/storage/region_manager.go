package storage

import (
	"container/list"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// RegionManager управляет открытыми регионами и их кешем
type RegionManager struct {
	basePath       string
	regions        map[string]*RegionFile
	regionsMutex   sync.RWMutex
	maxOpenRegions int
	lruList        *list.List
	lruMap         map[string]*list.Element

	// Фоновый воркер компактации
	stopChan chan struct{}
	wg       sync.WaitGroup

	dirtyRegions map[string]bool // регионы с несохранёнными изменениями
}

// LRU элемент для отслеживания использования регионов
type regionLRUItem struct {
	key        string // Ключ региона
	lastAccess time.Time
}

// NewRegionManager создаёт новый менеджер регионов
func NewRegionManager(basePath string) *RegionManager {
	rm := &RegionManager{
		basePath:       basePath,
		regions:        make(map[string]*RegionFile),
		maxOpenRegions: MaxOpenRegions, // Значение берётся из конфигурации
		lruList:        list.New(),
		lruMap:         make(map[string]*list.Element),

		stopChan:     make(chan struct{}),
		dirtyRegions: make(map[string]bool),
	}

	// Запускаем фоновый воркер компактации
	rm.wg.Add(1)
	go rm.compactionWorker()

	return rm
}

// Получение ключа региона по координатам чанка
func getRegionKeyFromChunk(pos *game.ChunkPosition) string {
	regionX := pos.X / 16
	if pos.X < 0 && pos.X%16 != 0 {
		regionX-- // Корректировка для отрицательных координат
	}

	regionY := pos.Y / 16
	if pos.Y < 0 && pos.Y%16 != 0 {
		regionY-- // Корректировка для отрицательных координат
	}

	return fmt.Sprintf("%d:%d", regionX, regionY)
}

// Получение координат региона из ключа
func getRegionCoordsFromKey(key string) (int32, int32) {
	var regionX, regionY int32
	fmt.Sscanf(key, "%d:%d", &regionX, &regionY)
	return regionX, regionY
}

// GetRegion получает или открывает регион по координатам чанка
func (rm *RegionManager) GetRegion(pos *game.ChunkPosition) (*RegionFile, error) {
	regionKey := getRegionKeyFromChunk(pos)

	// Проверяем наличие региона в кеше
	rm.regionsMutex.RLock()
	region, exists := rm.regions[regionKey]
	rm.regionsMutex.RUnlock()

	if exists {
		// Обновляем позицию в LRU кеше
		rm.updateLRU(regionKey)
		return region, nil
	}

	// Регион не найден в кеше, открываем его
	return rm.openRegion(regionKey)
}

// Открытие региона
func (rm *RegionManager) openRegion(regionKey string) (*RegionFile, error) {
	rm.regionsMutex.Lock()
	defer rm.regionsMutex.Unlock()

	// Проверяем ещё раз, не был ли регион открыт другой горутиной
	if region, exists := rm.regions[regionKey]; exists {
		rm.updateLRU(regionKey)
		return region, nil
	}

	// Проверяем, не превышен ли лимит открытых регионов
	if len(rm.regions) >= rm.maxOpenRegions {
		// Закрываем наименее используемый регион
		if err := rm.closeOldestRegion(); err != nil {
			return nil, fmt.Errorf("не удалось закрыть старый регион: %w", err)
		}
	}

	// Получаем координаты региона
	regionX, regionY := getRegionCoordsFromKey(regionKey)

	// Открываем файл региона
	region, err := NewRegionFile(rm.basePath, regionX, regionY)
	if err != nil {
		return nil, err
	}

	// Добавляем регион в кеш
	rm.regions[regionKey] = region

	// Добавляем в LRU кеш
	item := &regionLRUItem{
		key:        regionKey,
		lastAccess: time.Now(),
	}
	element := rm.lruList.PushFront(item)
	rm.lruMap[regionKey] = element

	return region, nil
}

// Закрытие самого старого региона
func (rm *RegionManager) closeOldestRegion() error {
	if rm.lruList.Len() == 0 {
		return nil // Нет регионов для закрытия
	}

	// Ищем самый старый не-грязный регион
	var selected *list.Element
	for e := rm.lruList.Back(); e != nil; e = e.Prev() {
		item := e.Value.(*regionLRUItem)
		if rm.dirtyRegions[item.key] {
			continue // пропускаем грязные
		}
		selected = e
		break
	}

	if selected == nil {
		return nil // все регионы грязные, не выгружаем
	}

	item := selected.Value.(*regionLRUItem)
	key := item.key

	// Получаем регион из кеша
	region, exists := rm.regions[key]
	if !exists {
		rm.lruList.Remove(selected)
		delete(rm.lruMap, key)
		return nil
	}

	// Синхронизируем перед выгрузкой
	if err := region.Sync(); err != nil {
		log.Printf("Ошибка при sync региона %s: %v", key, err)
	}

	// Закрываем файл региона
	if err := region.Close(); err != nil {
		return err
	}

	// Удаляем из кешей
	delete(rm.regions, key)
	rm.lruList.Remove(selected)
	delete(rm.lruMap, key)

	log.Printf("Закрыт неиспользуемый регион: %s", key)
	return nil
}

// Обновление позиции в LRU кеше
func (rm *RegionManager) updateLRU(key string) {
	element, exists := rm.lruMap[key]
	if !exists {
		// Создаем новый элемент в LRU
		item := &regionLRUItem{
			key:        key,
			lastAccess: time.Now(),
		}
		element = rm.lruList.PushFront(item)
		rm.lruMap[key] = element
		return
	}

	// Обновляем время доступа
	item := element.Value.(*regionLRUItem)
	item.lastAccess = time.Now()

	// Перемещаем элемент в начало списка
	rm.lruList.MoveToFront(element)
}

// GetChunkDelta получает дельту чанка из регионального хранилища
func (rm *RegionManager) GetChunkDelta(pos *game.ChunkPosition) (*ChunkDelta, error) {
	region, err := rm.GetRegion(pos)
	if err != nil {
		return nil, err
	}

	// Получаем дельту из региона
	return region.GetChunk(pos)
}

// SaveChunkDelta сохраняет дельту чанка в региональное хранилище
func (rm *RegionManager) SaveChunkDelta(delta *ChunkDelta) error {
	region, err := rm.GetRegion(delta.ChunkPos)
	if err != nil {
		return err
	}

	regionKey := getRegionKeyFromChunk(delta.ChunkPos)
	// Помечаем регион грязным на время записи
	rm.setDirty(regionKey, true)

	// Сохраняем дельту в регион
	err = region.SaveChunk(delta)

	// Снимаем флаг dirty вне зависимости от результатов записи (файл уже синхронизирован)
	rm.setDirty(regionKey, false)

	return err
}

// Close закрывает все открытые регионы
func (rm *RegionManager) Close() error {
	// Сначала останавливаем фоновый воркер, чтобы он не держал R-локи
	close(rm.stopChan)
	rm.wg.Wait()

	rm.regionsMutex.Lock()
	defer rm.regionsMutex.Unlock()

	var lastErr error

	// Закрываем все регионы
	for key, region := range rm.regions {
		if err := region.Close(); err != nil {
			log.Printf("Ошибка при закрытии региона %s: %v", key, err)
			lastErr = err
		}
	}

	// Очищаем кеши
	rm.regions = make(map[string]*RegionFile)
	rm.lruList = list.New()
	rm.lruMap = make(map[string]*list.Element)

	return lastErr
}

// compactionWorker периодически проверяет открытые файлы регионов и выполняет
// Compact() при необходимости.
func (rm *RegionManager) compactionWorker() {
	defer rm.wg.Done()

	ticker := time.NewTicker(RegionCompactionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rm.regionsMutex.RLock()
			for _, region := range rm.regions {
				if region.NeedsCompaction() {
					if err := region.Compact(); err != nil {
						log.Printf("Ошибка компактации региона %s: %v", region.filename, err)
					}
				}
			}
			rm.regionsMutex.RUnlock()
		case <-rm.stopChan:
			return
		}
	}
}

// setDirty помечает регион «грязным» или «чистым».
func (rm *RegionManager) setDirty(regionKey string, dirty bool) {
	rm.regionsMutex.Lock()
	defer rm.regionsMutex.Unlock()
	if dirty {
		rm.dirtyRegions[regionKey] = true
	} else {
		delete(rm.dirtyRegions, regionKey)
	}
}
