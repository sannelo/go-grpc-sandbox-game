package block

import (
	"math/rand"
	"strconv"
	"time"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
)

// Константы для динамических блоков
const (
	// Типы динамических блоков (расширение списка из chunkmanager)
	BlockTypeFire      = 20 // Огонь
	BlockTypeCrop      = 21 // Растущее растение (урожай)
	BlockTypeFlowWater = 22 // Текущая вода
	BlockTypeFlowLava  = 23 // Текущая лава

	// Свойства огня
	FirePropertyIntensity    = "intensity"    // Интенсивность огня (от 1 до 5)
	FirePropertyLifetime     = "lifetime"     // Время жизни огня (секунды)
	FirePropertySpreadChance = "spreadChance" // Шанс распространения (0-100)

	// Свойства растений
	CropPropertyStage      = "stage"      // Стадия роста (от 0 до 3)
	CropPropertyNextGrowth = "nextGrowth" // Время следующего роста (unix timestamp)
	CropPropertyWaterLevel = "waterLevel" // Уровень влаги (0-100)
	CropPropertyFertilizer = "fertilizer" // Количество удобрений (0-100)
	CropPropertyType       = "cropType"   // Тип урожая (пшеница, морковь, и т.д.)
)

// FireBlock представляет блок огня
type FireBlock struct {
	*BaseBlock
	intensity    int // Интенсивность огня (1-5)
	lifetime     int // Время жизни в секундах
	spreadChance int // Шанс распространения (0-100)
	rnd          *rand.Rand
}

// NewFireBlock создает новый блок огня
func NewFireBlock(x, y int32, chunkPos *game.ChunkPosition, blockType int32) Block {
	baseBlock := NewBaseBlock(x, y, chunkPos, BlockTypeFire)

	fire := &FireBlock{
		BaseBlock:    baseBlock,
		intensity:    3,
		lifetime:     60,
		spreadChance: 30,
		rnd:          rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	// Устанавливаем начальные свойства
	fire.SetProperty(FirePropertyIntensity, strconv.Itoa(fire.intensity))
	fire.SetProperty(FirePropertyLifetime, strconv.Itoa(fire.lifetime))
	fire.SetProperty(FirePropertySpreadChance, strconv.Itoa(fire.spreadChance))

	return fire
}

// Tick обновляет состояние огня
func (f *FireBlock) Tick(dt time.Duration, neighbors map[string]Block) bool {
	// Уменьшаем время жизни
	dtSeconds := int(dt.Seconds())
	if dtSeconds < 1 {
		dtSeconds = 1
	}

	f.lifetime -= dtSeconds
	f.SetProperty(FirePropertyLifetime, strconv.Itoa(f.lifetime))

	// Если время жизни истекло, огонь затухает
	if f.lifetime <= 0 {
		f.intensity = 0
		f.SetProperty(FirePropertyIntensity, "0")
		return true
	}

	// Случайно меняем интенсивность огня
	if f.rnd.Intn(100) < 20 {
		change := f.rnd.Intn(3) - 1 // -1, 0, 1
		f.intensity += change

		if f.intensity < 1 {
			f.intensity = 1
		} else if f.intensity > 5 {
			f.intensity = 5
		}

		f.SetProperty(FirePropertyIntensity, strconv.Itoa(f.intensity))
	}

	// Пытаемся распространиться на горючие блоки рядом
	if f.rnd.Intn(100) < f.spreadChance {
		// Логика распространения огня будет реализована в BlockManager
	}

	return true // Состояние изменилось
}

// GetScheduleInfo возвращает информацию о планировании обновлений
func (f *FireBlock) GetScheduleInfo() TickScheduleInfo {
	return TickScheduleInfo{
		MinInterval: 1 * time.Second,
		Priority:    4, // Высокий приоритет для огня
		NextTick:    time.Now().Add(1 * time.Second),
	}
}

// ToProto преобразует блок в протобуф-сообщение
func (f *FireBlock) ToProto() *game.Block {
	protoBlock := f.BaseBlock.ToProto()

	// Используем свойства для хранения информации о динамике блока
	protoBlock.Properties["_isDynamic"] = "true"
	protoBlock.Properties["_tickPriority"] = "4"
	protoBlock.Properties["_nextTick"] = strconv.FormatInt(time.Now().Add(1*time.Second).Unix(), 10)

	return protoBlock
}

// CropBlock представляет растущее растение
type CropBlock struct {
	*BaseBlock
	stage      int       // Стадия роста (0-3)
	nextGrowth time.Time // Время следующего роста
	waterLevel int       // Уровень влаги (0-100)
	fertilizer int       // Уровень удобрений (0-100)
	cropType   string    // Тип растения
	rnd        *rand.Rand
}

// NewCropBlock создает новый блок растения
func NewCropBlock(x, y int32, chunkPos *game.ChunkPosition, blockType int32) Block {
	baseBlock := NewBaseBlock(x, y, chunkPos, BlockTypeCrop)

	crop := &CropBlock{
		BaseBlock:  baseBlock,
		stage:      0,
		nextGrowth: time.Now().Add(5 * time.Minute),
		waterLevel: 50,
		fertilizer: 0,
		cropType:   "wheat", // По умолчанию пшеница
		rnd:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	// Устанавливаем начальные свойства
	crop.SetProperty(CropPropertyStage, strconv.Itoa(crop.stage))
	crop.SetProperty(CropPropertyNextGrowth, strconv.FormatInt(crop.nextGrowth.Unix(), 10))
	crop.SetProperty(CropPropertyWaterLevel, strconv.Itoa(crop.waterLevel))
	crop.SetProperty(CropPropertyFertilizer, strconv.Itoa(crop.fertilizer))
	crop.SetProperty(CropPropertyType, crop.cropType)

	return crop
}

// Tick обновляет состояние растения
func (c *CropBlock) Tick(dt time.Duration, neighbors map[string]Block) bool {
	now := time.Now()

	// Если ещё не время для роста, не делаем ничего
	if now.Before(c.nextGrowth) {
		return false
	}

	// Уменьшаем уровень влаги
	c.waterLevel -= c.rnd.Intn(5) + 1
	if c.waterLevel < 0 {
		c.waterLevel = 0
	}
	c.SetProperty(CropPropertyWaterLevel, strconv.Itoa(c.waterLevel))

	// Если недостаточно воды, растение не растет
	if c.waterLevel < 20 {
		// Устанавливаем новое время проверки
		c.nextGrowth = now.Add(30 * time.Minute)
		c.SetProperty(CropPropertyNextGrowth, strconv.FormatInt(c.nextGrowth.Unix(), 10))
		return true
	}

	// Растение растет, если это возможно
	if c.stage < 3 {
		c.stage++
		c.SetProperty(CropPropertyStage, strconv.Itoa(c.stage))

		// Рассчитываем время до следующего роста
		// Базовое время + случайный фактор - бонус от удобрений
		baseTime := 6 * time.Hour
		randomFactor := time.Duration(c.rnd.Intn(int(3 * time.Hour)))
		fertilizerBonus := time.Duration(c.fertilizer) * time.Minute

		growthTime := baseTime + randomFactor - fertilizerBonus
		if growthTime < 1*time.Hour {
			growthTime = 1 * time.Hour
		}

		c.nextGrowth = now.Add(growthTime)
		c.SetProperty(CropPropertyNextGrowth, strconv.FormatInt(c.nextGrowth.Unix(), 10))

		return true
	}

	// Если растение полностью выросло, устанавливаем большой интервал до следующей проверки
	c.nextGrowth = now.Add(24 * time.Hour)
	c.SetProperty(CropPropertyNextGrowth, strconv.FormatInt(c.nextGrowth.Unix(), 10))

	return false
}

// GetScheduleInfo возвращает информацию о планировании обновлений
func (c *CropBlock) GetScheduleInfo() TickScheduleInfo {
	// Вычисляем время до следующего обновления
	now := time.Now()
	waitTime := c.nextGrowth.Sub(now)

	if waitTime < 0 {
		waitTime = 0
	}

	return TickScheduleInfo{
		MinInterval: 5 * time.Minute, // Минимальный интервал между проверками
		Priority:    1,               // Низкий приоритет для растений
		NextTick:    now.Add(waitTime),
	}
}

// ToProto преобразует блок в протобуф-сообщение
func (c *CropBlock) ToProto() *game.Block {
	protoBlock := c.BaseBlock.ToProto()

	// Используем свойства для хранения информации о динамике блока
	protoBlock.Properties["_isDynamic"] = "true"
	protoBlock.Properties["_tickPriority"] = "1"
	protoBlock.Properties["_nextTick"] = strconv.FormatInt(c.nextGrowth.Unix(), 10)

	return protoBlock
}

// RegisterDynamicBlocks регистрирует фабрики для динамических блоков
func RegisterDynamicBlocks(manager *BlockManager) {
	manager.RegisterBlockFactory(BlockTypeFire, NewFireBlock)
	manager.RegisterBlockFactory(BlockTypeCrop, NewCropBlock)
}
