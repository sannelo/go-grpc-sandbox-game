package block

import (
	"testing"

	"github.com/annelo/go-grpc-server/pkg/protocol/game"
	"github.com/stretchr/testify/assert"
)

func TestBaseBlock(t *testing.T) {
	// Создаем базовый блок
	chunkPos := &game.ChunkPosition{X: 1, Y: 2}
	block := NewBaseBlock(3, 4, chunkPos, 5)

	// Проверяем, что ID генерируется корректно
	expectedID := "3:4:1:2"
	assert.Equal(t, expectedID, block.GetID(), "ID блока должен соответствовать формату x:y:chunkX:chunkY")

	// Проверяем получение позиции
	x, y, pos := block.GetPosition()
	assert.Equal(t, int32(3), x, "X-координата блока должна быть 3")
	assert.Equal(t, int32(4), y, "Y-координата блока должна быть 4")
	assert.Equal(t, chunkPos, pos, "Позиция чанка должна соответствовать")

	// Проверяем получение типа блока
	assert.Equal(t, int32(5), block.GetType(), "Тип блока должен быть 5")

	// Проверяем сериализацию/десериализацию
	// Устанавливаем свойства
	block.SetProperty("test", "value")
	block.SetProperty("number", "42")

	// Сериализуем блок
	data, err := block.Serialize()
	assert.NoError(t, err, "Сериализация не должна вызывать ошибок")

	// Создаем новый блок и десериализуем в него данные
	newBlock := NewBaseBlock(3, 4, chunkPos, 5)
	err = newBlock.Deserialize(data)
	assert.NoError(t, err, "Десериализация не должна вызывать ошибок")

	// Проверяем, что свойства сохранились
	val, exists := newBlock.GetProperty("test")
	assert.True(t, exists, "Свойство 'test' должно существовать")
	assert.Equal(t, "value", val, "Значение свойства 'test' должно быть 'value'")

	val, exists = newBlock.GetProperty("number")
	assert.True(t, exists, "Свойство 'number' должно существовать")
	assert.Equal(t, "42", val, "Значение свойства 'number' должно быть '42'")

	// Проверяем флаг изменений
	assert.True(t, block.HasChanges(), "После установки свойств блок должен быть помечен как измененный")
	block.ResetChanges()
	assert.False(t, block.HasChanges(), "После сброса флага изменений блок не должен быть помечен как измененный")
	block.MarkChanged()
	assert.True(t, block.HasChanges(), "После явной пометки блок должен быть помечен как измененный")

	// Проверяем конвертацию в protobuf
	protoBlock := block.ToProto()
	assert.Equal(t, int32(3), protoBlock.X, "X-координата в proto должна быть 3")
	assert.Equal(t, int32(4), protoBlock.Y, "Y-координата в proto должна быть 4")
	assert.Equal(t, int32(5), protoBlock.Type, "Тип блока в proto должен быть 5")
	assert.Equal(t, "value", protoBlock.Properties["test"], "Свойство 'test' в proto должно быть 'value'")
	assert.Equal(t, "42", protoBlock.Properties["number"], "Свойство 'number' в proto должно быть '42'")
}

func TestGetBlockID(t *testing.T) {
	// Проверяем генерацию ID блока
	chunkPos := &game.ChunkPosition{X: 10, Y: 20}
	id := GetBlockID(30, 40, chunkPos)
	expected := "30:40:10:20"
	assert.Equal(t, expected, id, "ID блока должен соответствовать формату x:y:chunkX:chunkY")
}
