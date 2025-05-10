package noisegeneration

import (
	"fmt"
	"math"
	"sync"

	"github.com/aquilax/go-perlin"
)

// CompactNoise представляет компактное целочисленное представление шума
type CompactNoise int8

// Константы для преобразования между float64 и CompactNoise
const (
	// Количество уровней шума для int8 (-128 до 127)
	NoiseResolution = 255
	// Минимальное значение шума
	MinNoiseValue = -1.0
	// Максимальное значение шума
	MaxNoiseValue = 1.0
)

// FloatToCompact преобразует float64 в CompactNoise
func FloatToCompact(value float64) CompactNoise {
	// Нормализуем значение к диапазону [0, 1]
	normalized := (value - MinNoiseValue) / (MaxNoiseValue - MinNoiseValue)
	// Масштабируем к диапазону [0, NoiseResolution]
	scaled := normalized * NoiseResolution
	// Преобразуем в целое число и ограничиваем диапазон от -127 до 127
	compact := int8(math.Min(127, math.Max(-127, math.Round(scaled)-128)))
	return CompactNoise(compact)
}

// CompactToFloat преобразует CompactNoise обратно в float64
func CompactToFloat(value CompactNoise) float64 {
	// Преобразуем в диапазон [0, NoiseResolution]
	scaled := float64(int8(value)) + 127.0
	// Нормализуем к диапазону [0, 1]
	normalized := scaled / NoiseResolution
	// Масштабируем обратно к исходному диапазону
	return normalized*(MaxNoiseValue-MinNoiseValue) + MinNoiseValue
}

// NoiseCache представляет собой кеш для значений шума
type NoiseCache struct {
	cache     map[string]CompactNoise // Кеш значений шума
	keys      []string                // Ключи в порядке использования (для LRU)
	capacity  int                     // Максимальная емкость кеша
	mu        sync.RWMutex            // Мьютекс для многопоточного доступа
	hitCount  int                     // Счетчик попаданий в кеш
	missCount int                     // Счетчик промахов кеша
}

// NewNoiseCache создает новый кеш значений шума
func NewNoiseCache(capacity int) *NoiseCache {
	return &NoiseCache{
		cache:    make(map[string]CompactNoise),
		keys:     make([]string, 0, capacity),
		capacity: capacity,
	}
}

// getCacheKey формирует ключ для кеша из координат и параметров
func getCacheKey(x, y float64, octaves int) string {
	return fmt.Sprintf("%.4f:%.4f:%d", x, y, octaves)
}

// Get получает значение из кеша, возвращает значение и флаг успеха
func (nc *NoiseCache) Get(x, y float64, octaves int) (float64, bool) {
	key := getCacheKey(x, y, octaves)

	nc.mu.RLock()
	compactValue, exists := nc.cache[key]
	nc.mu.RUnlock()

	if exists {
		nc.mu.Lock()
		nc.hitCount++
		// Обновляем порядок ключей (перемещаем использованный ключ в конец)
		nc.moveKeyToEnd(key)
		nc.mu.Unlock()

		// Преобразуем обратно в float64
		return CompactToFloat(compactValue), true
	} else {
		nc.mu.Lock()
		nc.missCount++
		nc.mu.Unlock()
	}

	return 0, false
}

// Put добавляет значение в кеш
func (nc *NoiseCache) Put(x, y float64, octaves int, value float64) {
	key := getCacheKey(x, y, octaves)

	// Преобразуем float64 в компактное представление
	compactValue := FloatToCompact(value)

	nc.mu.Lock()
	defer nc.mu.Unlock()

	// Если ключ уже существует, обновляем его
	if _, exists := nc.cache[key]; exists {
		nc.cache[key] = compactValue
		nc.moveKeyToEnd(key)
		return
	}

	// Проверяем, не достигли ли мы максимальной емкости
	if len(nc.cache) >= nc.capacity {
		// Удаляем наименее используемый элемент (первый в списке ключей)
		delete(nc.cache, nc.keys[0])
		nc.keys = nc.keys[1:]
	}

	// Добавляем новый элемент
	nc.cache[key] = compactValue
	nc.keys = append(nc.keys, key)
}

// moveKeyToEnd перемещает ключ в конец списка ключей (самый недавно использованный)
func (nc *NoiseCache) moveKeyToEnd(key string) {
	// Находим индекс ключа
	index := -1
	for i, k := range nc.keys {
		if k == key {
			index = i
			break
		}
	}

	if index >= 0 {
		// Удаляем ключ из текущей позиции
		nc.keys = append(nc.keys[:index], nc.keys[index+1:]...)
		// Добавляем ключ в конец
		nc.keys = append(nc.keys, key)
	}
}

// GetStats возвращает статистику использования кеша
func (nc *NoiseCache) GetStats() (int, int, float64) {
	nc.mu.RLock()
	defer nc.mu.RUnlock()

	total := nc.hitCount + nc.missCount
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(nc.hitCount) / float64(total)
	}

	return nc.hitCount, nc.missCount, hitRate
}

// ClearCache очищает кеш значений шума
func (nc *NoiseCache) ClearCache() {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	nc.cache = make(map[string]CompactNoise)
	nc.keys = make([]string, 0, nc.capacity)
}

// NoiseMap представляет карту шума для генерации ландшафта
type NoiseMap struct {
	perlin      *perlin.Perlin
	scale       float64     // Масштаб (чем меньше, тем более плавный ландшафт)
	octaves     int         // Количество октав (слоев шума)
	persistence float64     // Множитель амплитуды между октавами (обычно < 1.0)
	lacunarity  float64     // Множитель частоты между октавами (обычно > 1.0)
	min         float64     // Минимальное значение для нормализации
	max         float64     // Максимальное значение для нормализации
	cache       *NoiseCache // Кеш значений шума
}

// NewNoiseMap создает новую карту шума с заданными параметрами
func NewNoiseMap(seed int64, scale float64) *NoiseMap {
	// Параметры для perlin.NewPerlin:
	// alpha - персистентность (влияет на детализацию)
	// beta - лакунарность (влияет на частоту деталей)
	// n - количество октав (слоев шума)
	// seed - начальное значение для генерации
	alpha := 2.0
	beta := 2.0
	n := int32(3)

	return &NoiseMap{
		perlin:      perlin.NewPerlin(alpha, beta, n, seed),
		scale:       scale,
		octaves:     int(n),
		persistence: 0.5,
		lacunarity:  2.0,
		min:         -1.0,
		max:         1.0,
		cache:       NewNoiseCache(10000), // Кеш на 10000 значений
	}
}

// Get2D возвращает значение шума в заданной 2D точке
func (nm *NoiseMap) Get2D(x, y float64) float64 {
	// Проверяем наличие значения в кеше
	if value, exists := nm.cache.Get(x, y, 1); exists {
		return value
	}

	// Масштабируем координаты
	scaledX := x * nm.scale
	scaledY := y * nm.scale

	// Получаем базовое значение шума Перлина
	value := nm.perlin.Noise2D(scaledX, scaledY)

	// Кешируем результат
	nm.cache.Put(x, y, 1, value)

	return value
}

// GetOctave2D возвращает значение шума с заданным количеством октав
func (nm *NoiseMap) GetOctave2D(x, y float64, octaves int) float64 {
	// Проверяем наличие значения в кеше
	if value, exists := nm.cache.Get(x, y, octaves); exists {
		return value
	}

	// Масштабируем координаты
	scaledX := x * nm.scale
	scaledY := y * nm.scale

	amplitude := 1.0
	frequency := 1.0
	total := 0.0
	maxValue := 0.0 // Для нормализации

	// Суммируем октавы
	for i := 0; i < octaves; i++ {
		total += nm.perlin.Noise2D(scaledX*frequency, scaledY*frequency) * amplitude
		maxValue += amplitude

		amplitude *= nm.persistence
		frequency *= nm.lacunarity
	}

	// Нормализуем результат для диапазона от -1 до 1
	value := total / maxValue

	// Кешируем результат
	nm.cache.Put(x, y, octaves, value)

	return value
}

// GetNormalized2D возвращает нормализованное значение шума от 0 до 1
func (nm *NoiseMap) GetNormalized2D(x, y float64) float64 {
	// Получаем базовое значение шума
	noiseValue := nm.Get2D(x, y)

	// Нормализуем в диапазон [0.0, 1.0]
	return (noiseValue - nm.min) / (nm.max - nm.min)
}

// GetOctaveNormalized2D возвращает нормализованное значение шума для заданного числа октав
func (nm *NoiseMap) GetOctaveNormalized2D(x, y float64, octaves int) float64 {
	// Получаем значение шума с несколькими октавами
	noiseValue := nm.GetOctave2D(x, y, octaves)

	// Нормализуем в диапазон [0.0, 1.0]
	return (noiseValue - nm.min) / (nm.max - nm.min)
}

// SetScale устанавливает новый масштаб для карты шума
func (nm *NoiseMap) SetScale(scale float64) {
	nm.scale = scale
}

// SetOctaves устанавливает количество октав
func (nm *NoiseMap) SetOctaves(octaves int) {
	nm.octaves = octaves
}

// BiomeCache представляет оптимизированный кеш биомов
type BiomeCache struct {
	// Используем целочисленные ключи вместо строковых для экономии памяти
	cache     map[int64][3]CompactNoise // [height, moisture, temperature]
	keys      []int64                   // Ключи в порядке использования
	capacity  int                       // Максимальная емкость кеша
	mu        sync.RWMutex              // Мьютекс для доступа
	hitCount  int                       // Попадания в кеш
	missCount int                       // Промахи кеша
}

// NewBiomeCache создает новый оптимизированный кеш биомов
func NewBiomeCache(capacity int) *BiomeCache {
	return &BiomeCache{
		cache:    make(map[int64][3]CompactNoise),
		keys:     make([]int64, 0, capacity),
		capacity: capacity,
	}
}

// getBiomeCacheKey генерирует целочисленный ключ из координат
func getBiomeCacheKey(x, y float64) int64 {
	// Преобразуем координаты в целые числа
	ix := int32(math.Floor(x))
	iy := int32(math.Floor(y))

	// Объединяем в один 64-битный ключ
	return (int64(ix) << 32) | (int64(iy) & 0xFFFFFFFF)
}

// Get получает данные о биоме из кеша
func (bc *BiomeCache) Get(x, y float64) (height, moisture, temperature float64, exists bool) {
	key := getBiomeCacheKey(x, y)

	bc.mu.RLock()
	values, found := bc.cache[key]
	bc.mu.RUnlock()

	if found {
		bc.mu.Lock()
		bc.hitCount++
		// Обновляем порядок ключей
		bc.moveKeyToEnd(key)
		bc.mu.Unlock()

		// Преобразуем обратно в float64
		return CompactToFloat(values[0]),
			CompactToFloat(values[1]),
			CompactToFloat(values[2]),
			true
	}

	bc.mu.Lock()
	bc.missCount++
	bc.mu.Unlock()

	return 0, 0, 0, false
}

// Put сохраняет данные о биоме в кеш
func (bc *BiomeCache) Put(x, y float64, height, moisture, temperature float64) {
	key := getBiomeCacheKey(x, y)

	// Преобразуем в компактный формат
	values := [3]CompactNoise{
		FloatToCompact(height),
		FloatToCompact(moisture),
		FloatToCompact(temperature),
	}

	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Если ключ уже существует, обновляем его
	if _, exists := bc.cache[key]; exists {
		bc.cache[key] = values
		bc.moveKeyToEnd(key)
		return
	}

	// Проверяем, не достигли ли мы максимальной емкости
	if len(bc.cache) >= bc.capacity {
		// Удаляем наименее используемый элемент
		delete(bc.cache, bc.keys[0])
		bc.keys = bc.keys[1:]
	}

	// Добавляем новый элемент
	bc.cache[key] = values
	bc.keys = append(bc.keys, key)
}

// moveKeyToEnd перемещает ключ в конец списка ключей
func (bc *BiomeCache) moveKeyToEnd(key int64) {
	// Находим индекс ключа
	index := -1
	for i, k := range bc.keys {
		if k == key {
			index = i
			break
		}
	}

	if index >= 0 {
		// Удаляем ключ из текущей позиции
		bc.keys = append(bc.keys[:index], bc.keys[index+1:]...)
		// Добавляем ключ в конец
		bc.keys = append(bc.keys, key)
	}
}

// GetStats возвращает статистику использования кеша
func (bc *BiomeCache) GetStats() (int, int, float64) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	total := bc.hitCount + bc.missCount
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(bc.hitCount) / float64(total)
	}

	return bc.hitCount, bc.missCount, hitRate
}

// ClearCache очищает кеш биомов
func (bc *BiomeCache) ClearCache() {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	bc.cache = make(map[int64][3]CompactNoise)
	bc.keys = make([]int64, 0, bc.capacity)
}

// BiomeNoise возвращает значение шума для определения типа биома
type BiomeNoise struct {
	heightMap      *NoiseMap   // Карта высот
	moistureMap    *NoiseMap   // Карта влажности
	temperatureMap *NoiseMap   // Карта температуры
	biomeCache     *BiomeCache // Оптимизированный кеш биомов
}

// NewBiomeNoise создает новый генератор шума для биомов
func NewBiomeNoise(seed int64) *BiomeNoise {
	return &BiomeNoise{
		heightMap:      NewNoiseMap(seed, 0.01),    // Масштаб для высоты
		moistureMap:    NewNoiseMap(seed+1, 0.005), // Масштаб для влажности (более плавный)
		temperatureMap: NewNoiseMap(seed+2, 0.008), // Масштаб для температуры
		biomeCache:     NewBiomeCache(10000),       // Кеш на 10000 значений биомов
	}
}

// GetBiomeData возвращает данные о биоме в заданной точке
func (bn *BiomeNoise) GetBiomeData(x, y float64) (height, moisture, temperature float64) {
	// Проверяем наличие данных в кеше
	cachedHeight, cachedMoisture, cachedTemperature, exists := bn.biomeCache.Get(x, y)
	if exists {
		return cachedHeight, cachedMoisture, cachedTemperature
	}

	// Если данных нет в кеше, вычисляем их
	height = bn.heightMap.GetOctaveNormalized2D(x, y, 4)
	moisture = bn.moistureMap.GetOctaveNormalized2D(x, y, 2)
	temperature = bn.temperatureMap.GetOctaveNormalized2D(x, y, 3)

	// Сохраняем в кеш
	bn.biomeCache.Put(x, y, height, moisture, temperature)

	return height, moisture, temperature
}

// GetCacheStats возвращает статистику использования кешей
func (bn *BiomeNoise) GetCacheStats() map[string]interface{} {
	heightHits, heightMisses, heightRate := bn.heightMap.cache.GetStats()
	moistureHits, moistureMisses, moistureRate := bn.moistureMap.cache.GetStats()
	tempHits, tempMisses, tempRate := bn.temperatureMap.cache.GetStats()
	biomeHits, biomeMisses, biomeRate := bn.biomeCache.GetStats()

	return map[string]interface{}{
		"height": map[string]interface{}{
			"hits":     heightHits,
			"misses":   heightMisses,
			"hit_rate": heightRate,
		},
		"moisture": map[string]interface{}{
			"hits":     moistureHits,
			"misses":   moistureMisses,
			"hit_rate": moistureRate,
		},
		"temperature": map[string]interface{}{
			"hits":     tempHits,
			"misses":   tempMisses,
			"hit_rate": tempRate,
		},
		"biome": map[string]interface{}{
			"hits":     biomeHits,
			"misses":   biomeMisses,
			"hit_rate": biomeRate,
			"size":     len(bn.biomeCache.cache),
		},
	}
}

// ClearAllCaches очищает все кеши в генераторе биомов
func (bn *BiomeNoise) ClearAllCaches() {
	bn.heightMap.cache.ClearCache()
	bn.moistureMap.cache.ClearCache()
	bn.temperatureMap.cache.ClearCache()
	bn.biomeCache.ClearCache()
}

// IsWater определяет, является ли точка водой на основе высоты
func IsWater(height float64) bool {
	return height < 0.4 // Порог для воды
}

// BiomeType представляет тип биома
type BiomeType int

const (
	BiomeOcean BiomeType = iota
	BiomeBeach
	BiomeDesert
	BiomelPlains
	BiomeForest
	BiomeTaiga
	BiomeMountain
	BiomeSnowland
)

// GetBiomeType определяет тип биома на основе высоты, влажности и температуры
func GetBiomeType(height, moisture, temperature float64) BiomeType {
	if height < 0.3 {
		return BiomeOcean
	}

	if height < 0.4 {
		return BiomeBeach
	}

	if height > 0.75 {
		if temperature < 0.3 {
			return BiomeSnowland
		}
		return BiomeMountain
	}

	if temperature < 0.3 {
		return BiomeTaiga
	}

	if temperature > 0.7 && moisture < 0.3 {
		return BiomeDesert
	}

	if moisture > 0.6 {
		return BiomeForest
	}

	return BiomelPlains
}
