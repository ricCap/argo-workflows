package util

import (
	"k8s.io/client-go/tools/cache"
)

// Indexer is a minimal cache.Indexer for tests; unimplemented methods panic
// via the embedded nil interface.
type Indexer struct {
	cache.Indexer
	byIndex map[string][]any
	byKey   map[string]any
}

var _ cache.Indexer = &Indexer{}

func NewIndexer() *Indexer {
	return &Indexer{byIndex: make(map[string][]any), byKey: make(map[string]any)}
}

func (i Indexer) GetByKey(key string) (item any, exists bool, err error) {
	obj, ok := i.byKey[key]
	return obj, ok, nil
}
func (i Indexer) SetByKey(key string, item any) { i.byKey[key] = item }
func (i Indexer) SetByIndex(indexName, indexedValue string, objs ...any) {
	i.byIndex[indexName+"="+indexedValue] = objs
}

func (i Indexer) ByIndex(indexName, indexedValue string) ([]any, error) {
	return i.byIndex[indexName+"="+indexedValue], nil
}
