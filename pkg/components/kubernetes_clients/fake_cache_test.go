package kubernetes_clients

import (
	"context"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// clientAsCache adapts a client.Client to satisfy cache.Cache for testing.
// Only Get and List are implemented; all other methods panic or no-op.
type clientAsCache struct{ client.Client }

var _ cache.Cache = clientAsCache{}

func (clientAsCache) GetInformer(_ context.Context, _ client.Object, _ ...cache.InformerGetOption) (cache.Informer, error) {
	panic("not implemented in test fake")
}

func (clientAsCache) GetInformerForKind(_ context.Context, _ schema.GroupVersionKind, _ ...cache.InformerGetOption) (cache.Informer, error) {
	panic("not implemented in test fake")
}

func (clientAsCache) RemoveInformer(_ context.Context, _ client.Object) error {
	panic("not implemented in test fake")
}

func (clientAsCache) Start(_ context.Context) error {
	panic("not implemented in test fake")
}

func (clientAsCache) WaitForCacheSync(_ context.Context) bool {
	panic("not implemented in test fake")
}

func (clientAsCache) IndexField(_ context.Context, _ client.Object, _ string, _ client.IndexerFunc) error {
	return nil
}

// newFakeCache returns a cache.Cache backed by a fake client with the UUID
// field indexes registered, pre-populated with objects.
func newFakeCache(t *testing.T, objects ...client.Object) cache.Cache {
	t.Helper()
	return clientAsCache{newFakeClientWithUUIDIndex(t, objects...)}
}
