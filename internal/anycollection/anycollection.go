package anycollection

import (
	"sync"
)

// Collection это sync.Map-ка, хранящая в себе произвольные итемы.
type Collection struct {
	items sync.Map
	close chan struct{}
}

// Item представляет собой интерфейс для хранения произвольных данных.
type item struct {
	data any
}

// NewCollection создаёт инстанс коллекции.
func NewCollection() *Collection {
	c := &Collection{
		items: sync.Map{},
		close: make(chan struct{}),
	}

	return c
}

// Get достаёт данные по заданному ключу из коллекции.
func (collection *Collection) Get(key any) (any, bool) {
	obj, exists := collection.items.Load(key)

	if !exists {
		return nil, false
	}

	item := obj.(item)

	return item.data, true
}

// Set сохраняет данные с заданным ключом в коллекцию.
func (collection *Collection) Set(key any, value any) {
	collection.items.Store(key, item{
		data: value,
	})
}

// Range применяет функцию f ко всем ключам в коллекции.
func (collection *Collection) Range(f func(key, value any) bool) {
	fn := func(key, value any) bool {
		item := value.(item)

		return f(key, item.data)
	}

	collection.items.Range(fn)
}

// Delete удаляет ключ и значение из коллекции данных.
func (collection *Collection) Delete(key any) {
	collection.items.Delete(key)
}

// Close очищает и высвобождает ресурсы, занятые коллекцией.
func (collection *Collection) Close() {
	collection.close <- struct{}{}
	collection.items = sync.Map{}
}

/* vim: set ft=go noet ai ts=4 sw=4 sts=4: */
