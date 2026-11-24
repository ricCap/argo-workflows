package util

import (
	"time"

	"k8s.io/client-go/tools/cache"
)

// SharedIndexInformer is a minimal cache.SharedIndexInformer for tests, backed
// by Indexer; unimplemented methods panic via the embedded nil interface.
type SharedIndexInformer struct {
	cache.SharedIndexInformer
	Indexer *Indexer
}

var _ cache.SharedIndexInformer = &SharedIndexInformer{}

func NewSharedIndexInformer() *SharedIndexInformer {
	return &SharedIndexInformer{Indexer: NewIndexer()}
}

func (s *SharedIndexInformer) AddEventHandler(cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}
func (s *SharedIndexInformer) AddEventHandlerWithResyncPeriod(cache.ResourceEventHandler, time.Duration) (cache.ResourceEventHandlerRegistration, error) {
	return nil, nil
}
func (s *SharedIndexInformer) RemoveEventHandler(cache.ResourceEventHandlerRegistration) error {
	return nil
}
func (s *SharedIndexInformer) GetStore() cache.Store                              { return s.Indexer }
func (s *SharedIndexInformer) GetIndexer() cache.Indexer                          { return s.Indexer }
func (s *SharedIndexInformer) AddIndexers(cache.Indexers) error                   { return nil }
func (s *SharedIndexInformer) Run(<-chan struct{})                                {}
func (s *SharedIndexInformer) HasSynced() bool                                    { return true }
func (s *SharedIndexInformer) LastSyncResourceVersion() string                    { return "" }
func (s *SharedIndexInformer) SetWatchErrorHandler(cache.WatchErrorHandler) error { return nil }
func (s *SharedIndexInformer) SetTransform(cache.TransformFunc) error             { return nil }
func (s *SharedIndexInformer) IsStopped() bool                                    { return false }
