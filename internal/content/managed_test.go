package content

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/storage"
)

type managedLookup struct{ component component.Discoverable }

func (l *managedLookup) Component(string) component.Discoverable { return l.component }

type managedProvider struct {
	stores map[string]storage.StreamableStore
}

func (p *managedProvider) ProvidedStores() map[string]storage.StreamableStore { return p.stores }
func (*managedProvider) Meta() component.Metadata                             { return component.Metadata{} }
func (*managedProvider) InputPorts() []component.Port                         { return nil }
func (*managedProvider) OutputPorts() []component.Port                        { return nil }
func (*managedProvider) ConfigSchema() component.ConfigSchema                 { return component.ConfigSchema{} }
func (*managedProvider) Health() component.HealthStatus                       { return component.HealthStatus{} }
func (*managedProvider) DataFlow() component.FlowMetrics                      { return component.FlowMetrics{} }

type managedNonProvider struct{}

func (*managedNonProvider) Meta() component.Metadata             { return component.Metadata{} }
func (*managedNonProvider) InputPorts() []component.Port         { return nil }
func (*managedNonProvider) OutputPorts() []component.Port        { return nil }
func (*managedNonProvider) ConfigSchema() component.ConfigSchema { return component.ConfigSchema{} }
func (*managedNonProvider) Health() component.HealthStatus       { return component.HealthStatus{} }
func (*managedNonProvider) DataFlow() component.FlowMetrics      { return component.FlowMetrics{} }

type memoryStreamStore struct{ values map[string][]byte }

func (s *memoryStreamStore) Put(_ context.Context, key string, value []byte) error {
	s.values[key] = bytes.Clone(value)
	return nil
}
func (s *memoryStreamStore) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := s.values[key]
	if !ok {
		return nil, storage.ErrObjectNotFound
	}
	return bytes.Clone(value), nil
}
func (s *memoryStreamStore) List(context.Context, string) ([]string, error) { return nil, nil }
func (s *memoryStreamStore) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}
func (s *memoryStreamStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	value, err := s.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(value)), nil
}

func TestManagedBackendUsesLogicalObjectstoreAndBorrowsCurrentGeneration(t *testing.T) {
	first := &memoryStreamStore{values: map[string][]byte{"key": []byte("first")}}
	second := &memoryStreamStore{values: map[string][]byte{"key": []byte("second")}}
	lookup := &managedLookup{component: &managedProvider{stores: map[string]storage.StreamableStore{
		ManagedStorageInstance: first,
	}}}
	backend := &ManagedBackend{components: lookup, physicalBucket: "PHYSICAL"}
	store, err := NewStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	if store.InstanceName() != ManagedStorageInstance {
		t.Fatalf("logical instance = %q", store.InstanceName())
	}
	value, err := backend.Get(t.Context(), "key")
	if err != nil || string(value) != "first" {
		t.Fatalf("first generation = %q, %v", value, err)
	}
	lookup.component = &managedProvider{stores: map[string]storage.StreamableStore{ManagedStorageInstance: second}}
	value, err = backend.Get(t.Context(), "key")
	if err != nil || string(value) != "second" {
		t.Fatalf("replacement generation = %q, %v", value, err)
	}
}

func TestManagedBackendFailsClosedWithoutExactLiveProvider(t *testing.T) {
	for _, tc := range []struct {
		name      string
		component component.Discoverable
		want      string
	}{
		{name: "missing", want: "not admitted"},
		{name: "wrong kind", component: &managedNonProvider{}, want: "not a StoreProvider"},
		{name: "stopped", component: &managedProvider{}, want: "not started"},
		{name: "wrong instance", component: &managedProvider{stores: map[string]storage.StreamableStore{
			"physical": &memoryStreamStore{values: map[string][]byte{}},
		}}, want: "not started"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &ManagedBackend{components: &managedLookup{component: tc.component}, physicalBucket: "PHYSICAL"}
			_, err := backend.Get(t.Context(), "key")
			if err == nil || !bytes.Contains([]byte(err.Error()), []byte(tc.want)) {
				t.Fatalf("Get error = %v, want %q", err, tc.want)
			}
		})
	}
}
