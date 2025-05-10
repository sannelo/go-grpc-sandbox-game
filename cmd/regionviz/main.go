package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/annelo/go-grpc-server/internal/chunkmanager"
	"github.com/annelo/go-grpc-server/internal/storage"
	util "github.com/annelo/go-grpc-server/internal/storage/util"
	"github.com/annelo/go-grpc-server/pkg/protocol/game"
	termbox "github.com/nsf/termbox-go"
)

// Размер чанка в блоках (дублируем константу, чтобы не тянуть весь пакет)
const chunkSize = chunkmanager.ChunkSize

var (
	worldPath = flag.String("path", "./data/regions", "Путь до директории с region-файлами")
	regionX   = flag.Int("x", -9999, "Координата региона X (если -1, вычисляется из startX/ startY)")
	regionY   = flag.Int("y", -9999, "Координата региона Y")
	zoom      = flag.Int("zoom", 1, "Коэффициент масштабирования блока -> символов (1-4)")
	startX    = flag.Int("startX", 1000, "Начальная мировая координата X камеры")
	startY    = flag.Int("startY", 1000, "Начальная мировая координата Y камеры")
)

// Чанк, загруженный из файла
type chunkData struct {
	delta *storage.ChunkDelta
}

func main() {
	flag.Parse()

	// Инициализируем termbox
	if err := termbox.Init(); err != nil {
		log.Fatalf("termbox init error: %v", err)
	}
	defer termbox.Close()

	// Если регион не задан явно, вычисляем его из стартовых координат
	if *regionX == -9999 || *regionY == -9999 {
		chunkX := int32(*startX) / chunkSize
		chunkY := int32(*startY) / chunkSize
		*regionX = int(chunkX / 16)
		*regionY = int(chunkY / 16)
	}

	// Открываем файл региона
	rf, err := storage.NewRegionFile(*worldPath, int32(*regionX), int32(*regionY))
	if err != nil {
		log.Fatalf("cannot open region file: %v", err)
	}
	defer rf.Close()

	// Кеш чанков в памяти
	chunkCache := make(map[string]*storage.ChunkDelta)

	// Позиция камеры и курсора
	var camX, camY int32 = int32(*startX), int32(*startY)
	var curX, curY int = 0, 0 // экранные координаты курсора

	draw := func() {
		termbox.Clear(termbox.ColorDefault, termbox.ColorDefault)

		width, height := termbox.Size()
		sx := *zoom
		sy := *zoom

		// Проходим по экранным координатам и выводим блоки
		for py := 0; py < height; py += sy {
			for px := 0; px < width; px += sx {
				worldX := camX + int32(px/sx)
				worldY := camY + int32(py/sy)

				// Получаем тип блока
				bt := getBlockType(rf, chunkCache, worldX, worldY)

				ch, fg, bg := blockSymbol(bt)

				// Рисуем символы в пределах зума
				for dy := 0; dy < sy; dy++ {
					for dx := 0; dx < sx; dx++ {
						termbox.SetCell(px+dx, py+dy, ch, fg, bg)
					}
				}
			}
		}

		// Выделяем курсор (инвертируем цвета)
		if curX < width && curY < height {
			cell := termbox.CellBuffer()[curY*width+curX]
			termbox.SetCell(curX, curY, cell.Ch, cell.Bg|termbox.AttrBold, cell.Fg)
		}

		// Заголовок
		header := fmt.Sprintf("Region (%d,%d)  Cam=(%d,%d)  Zoom=%dx", *regionX, *regionY, camX, camY, *zoom)
		for i, r := range header {
			termbox.SetCell(i, 0, r, termbox.ColorYellow|termbox.AttrBold, termbox.ColorBlack)
		}

		// Информация о блоке под курсором
		wx := camX + int32(curX/sx)
		wy := camY + int32(curY/sy)
		bt := getBlockType(rf, chunkCache, wx, wy)
		mod, last := blockMeta(rf, chunkCache, wx, wy)
		info := fmt.Sprintf("Block (%d,%d) Type=%d Mod=%v Last=%s", wx, wy, bt, mod, last)
		for i, r := range info {
			if i >= width {
				break
			}
			termbox.SetCell(i, 1, r, termbox.ColorWhite, termbox.ColorBlack)
		}

		termbox.Flush()
	}

	draw()

	// Основной цикл
	for {
		switch ev := termbox.PollEvent(); ev.Type {
		case termbox.EventKey:
			switch ev.Key {
			case termbox.KeyEsc, termbox.KeyCtrlC:
				return
			case termbox.KeyArrowLeft:
				camX -= 1
			case termbox.KeyArrowRight:
				camX += 1
			case termbox.KeyArrowUp:
				camY -= 1
			case termbox.KeyArrowDown:
				camY += 1
			default:
				if ev.Ch == 'q' {
					return
				}
				if ev.Ch == '+' && *zoom < 4 {
					*zoom++
				}
				if ev.Ch == '-' && *zoom > 1 {
					*zoom--
				}
				// WASD для курсора
				width, height := termbox.Size()
				sx, sy := *zoom, *zoom
				if ev.Ch == 'a' && curX > 0 {
					curX -= sx
				}
				if ev.Ch == 'd' && curX < width-sx {
					curX += sx
				}
				if ev.Ch == 'w' && curY > 0 {
					curY -= sy
				}
				if ev.Ch == 's' && curY < height-sy {
					curY += sy
				}
			}
			draw()
		case termbox.EventError:
			log.Printf("termbox error: %v", ev.Err)
			return
		case termbox.EventResize:
			draw()
		}
	}
}

// Возвращает символ и цвета для типа блока
func blockSymbol(bt int32) (rune, termbox.Attribute, termbox.Attribute) {
	switch bt {
	case chunkmanager.BlockTypeGrass:
		return '_', termbox.ColorGreen, termbox.ColorBlack
	case chunkmanager.BlockTypeDirt:
		return '.', termbox.ColorYellow, termbox.ColorBlack
	case chunkmanager.BlockTypeStone:
		return '#', termbox.ColorWhite, termbox.ColorBlack
	case chunkmanager.BlockTypeWater:
		return '~', termbox.ColorBlue, termbox.ColorBlack
	case chunkmanager.BlockTypeSand:
		return ',', termbox.ColorYellow, termbox.ColorBlack
	case chunkmanager.BlockTypeWood:
		return '|', termbox.ColorRed, termbox.ColorBlack
	case chunkmanager.BlockTypeLeaves:
		return '@', termbox.ColorGreen, termbox.ColorBlack
	case chunkmanager.BlockTypeSnow:
		return '*', termbox.ColorWhite, termbox.ColorBlue
	case chunkmanager.BlockTypeTallGrass:
		return '"', termbox.ColorGreen, termbox.ColorBlack
	case chunkmanager.BlockTypeFlower:
		return 'f', termbox.ColorMagenta, termbox.ColorBlack
	default:
		return ' ', termbox.ColorDefault, termbox.ColorDefault
	}
}

// Получаем тип блока в мировых координатах из региона
func getBlockType(rf *storage.RegionFile, cache map[string]*storage.ChunkDelta, wx, wy int32) int32 {
	// Проверяем, в пределах ли координаты текущего региона
	regionChunkX := int32(*regionX) * 16
	regionChunkY := int32(*regionY) * 16

	chunkX := wx / chunkSize
	chunkY := wy / chunkSize

	if chunkX < regionChunkX || chunkX > regionChunkX+15 || chunkY < regionChunkY || chunkY > regionChunkY+15 {
		return chunkmanager.BlockTypeAir
	}

	// Локальные координаты блока внутри чанка
	localX := wx % chunkSize
	localY := wy % chunkSize
	if localX < 0 {
		localX += chunkSize
	}
	if localY < 0 {
		localY += chunkSize
	}

	// Загружаем чанк из кеша либо из файла
	ckey := fmt.Sprintf("%d:%d", chunkX, chunkY)
	delta, ok := cache[ckey]
	if !ok {
		d, err := rf.GetChunk(&game.ChunkPosition{X: chunkX, Y: chunkY})
		if err != nil {
			// Нет данных о чанке
			cache[ckey] = nil
			return chunkmanager.BlockTypeAir
		}
		delta = d
		cache[ckey] = delta
	}
	if delta == nil {
		return chunkmanager.BlockTypeAir
	}

	key := util.BlockKey(localX, localY)
	if blk, exists := delta.BlockChanges[key]; exists {
		return blk.Type
	}
	return chunkmanager.BlockTypeAir
}

// blockMeta возвращает (isModified, lastModified)
func blockMeta(rf *storage.RegionFile, cache map[string]*storage.ChunkDelta, wx, wy int32) (bool, string) {
	chunkX := wx / chunkSize
	chunkY := wy / chunkSize

	key := fmt.Sprintf("%d:%d", chunkX, chunkY)
	delta, ok := cache[key]
	if !ok {
		d, err := rf.GetChunk(&game.ChunkPosition{X: chunkX, Y: chunkY})
		if err != nil {
			cache[key] = nil
			return false, "-"
		}
		delta = d
		cache[key] = delta
	}
	if delta == nil {
		return false, "-"
	}

	lx := wx % chunkSize
	ly := wy % chunkSize
	blkKey := util.BlockKey(lx, ly)
	if _, exists := delta.BlockChanges[blkKey]; exists {
		return true, delta.LastModified.Format(time.RFC822)
	}
	return false, "-"
}
