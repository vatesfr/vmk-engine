package tag_db

import (
	"context"
	"sync"

	"capv-proxy/pkg/strategies"
)

type TagDB struct {
	mu   sync.RWMutex
	tags map[string]map[string]string
}

var _ strategies.ForManagingClusterTags = (*TagDB)(nil)

func NewTagDB() *TagDB {
	return &TagDB{tags: make(map[string]map[string]string)}
}

func (db *TagDB) GetClusterTags(_ context.Context, clusterID string) map[string]string {
	db.mu.RLock()
	defer db.mu.RUnlock()

	result := make(map[string]string, len(db.tags[clusterID]))
	for name, value := range db.tags[clusterID] {
		result[name] = value
	}
	return result
}

func (db *TagDB) GetClusterTag(_ context.Context, clusterID, tag string) string {
	db.mu.RLock()
	defer db.mu.RUnlock()

	return db.tags[clusterID][tag]
}

func (db *TagDB) SetClusterTag(_ context.Context, clusterID, tag, value string) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.tags[clusterID] == nil {
		db.tags[clusterID] = make(map[string]string)
	}
	db.tags[clusterID][tag] = value
}

func (db *TagDB) DeleteClusterTag(_ context.Context, clusterID, tag string) {
	db.mu.Lock()
	defer db.mu.Unlock()

	delete(db.tags[clusterID], tag)
}
