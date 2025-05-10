package main

import (
	"fmt"
	"time"

	"github.com/annelo/go-grpc-server/internal/noisegeneration"
)

const (
	width  = 40
	height = 20
)

func main() {
	// Инициализируем генератор случайных чисел с текущим временем в качестве seed
	seed := time.Now().UnixNano()
	fmt.Printf("Seed: %d\n", seed)

	// Создаем генератор шума с различными параметрами
	biomeNoise := noisegeneration.NewBiomeNoise(seed)

	// Визуализируем карту высот
	fmt.Println("\nКарта высот:")
	visualizeHeightMap(biomeNoise)

	// Визуализируем карту биомов
	fmt.Println("\nКарта биомов:")
	visualizeBiomeMap(biomeNoise)
}

// visualizeHeightMap визуализирует карту высот шума Перлина
func visualizeHeightMap(noise *noisegeneration.BiomeNoise) {
	// Символы для различных высот от низкой к высокой
	chars := []rune{'~', '.', '-', '=', '#', '^', '*', '@'}

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// Получаем данные о биоме
			height, _, _ := noise.GetBiomeData(float64(x)*5, float64(y)*5)

			// Определяем символ для визуализации на основе высоты
			idx := int(height * float64(len(chars)-1))
			if idx >= len(chars) {
				idx = len(chars) - 1
			}

			fmt.Print(string(chars[idx]))
		}
		fmt.Println()
	}
}

// visualizeBiomeMap визуализирует карту биомов
func visualizeBiomeMap(noise *noisegeneration.BiomeNoise) {
	// Символы для различных биомов
	biomeChars := map[noisegeneration.BiomeType]rune{
		noisegeneration.BiomeOcean:    '~', // вода
		noisegeneration.BiomeBeach:    ',', // песок
		noisegeneration.BiomeDesert:   '.', // пустыня
		noisegeneration.BiomelPlains:  '_', // равнины
		noisegeneration.BiomeForest:   'f', // лес
		noisegeneration.BiomeTaiga:    't', // тайга
		noisegeneration.BiomeMountain: '^', // горы
		noisegeneration.BiomeSnowland: '*', // снежная земля
	}

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// Получаем данные о биоме
			height, moisture, temperature := noise.GetBiomeData(float64(x)*5, float64(y)*5)

			// Определяем тип биома
			biome := noisegeneration.GetBiomeType(height, moisture, temperature)

			// Выводим символ для соответствующего биома
			fmt.Print(string(biomeChars[biome]))
		}
		fmt.Println()
	}
}
