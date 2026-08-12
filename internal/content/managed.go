package content

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/c360studio/semstreams/component"
	"github.com/c360studio/semstreams/natsclient"
	"github.com/c360studio/semstreams/storage"
	"github.com/nats-io/nats.go/jetstream"
)

// ManagedStorageInstance is the logical name of the ComponentManager-owned
// ObjectStore. It is intentionally independent from the physical bucket name.
const ManagedStorageInstance = "objectstore"

// ManagedBackend borrows the live store owned by ComponentManager on every
// operation. It never caches or closes that handle, so component replacement
// and shutdown cannot leave product clients holding a retired generation.
// Only the exact-claim KV sidecar is local to this adapter.
type ManagedBackend struct {
	client         *natsclient.Client
	components     component.Lookup
	physicalBucket string

	claimsMu sync.Mutex
	claims   jetstream.KeyValue
}

var _ Backend = (*ManagedBackend)(nil)
var _ ExactResidentBackend = (*ManagedBackend)(nil)

// NewManagedBackend builds a lazy adapter over the manager-owned provider.
func NewManagedBackend(client *natsclient.Client, components component.Lookup, physicalBucket string) (*ManagedBackend, error) {
	if client == nil {
		return nil, errors.New("managed content backend requires a NATS client")
	}
	if components == nil {
		return nil, errors.New("managed content backend requires component lookup")
	}
	if err := validateInstanceName(physicalBucket); err != nil {
		return nil, err
	}
	return &ManagedBackend{client: client, components: components, physicalBucket: physicalBucket}, nil
}

// InstanceName returns the logical name of the manager-owned object store.
func (b *ManagedBackend) InstanceName() string { return ManagedStorageInstance }

func (b *ManagedBackend) providerStore() (storage.StreamableStore, error) {
	discoverable := b.components.Component(ManagedStorageInstance)
	if discoverable == nil {
		return nil, errors.New("managed content provider is not admitted")
	}
	provider, ok := discoverable.(component.StoreProvider)
	if !ok {
		return nil, fmt.Errorf("managed content component %q is a %T, not a StoreProvider",
			ManagedStorageInstance, discoverable)
	}
	store := provider.ProvidedStores()[ManagedStorageInstance]
	if store == nil {
		return nil, errors.New("managed content provider is not started")
	}
	return store, nil
}

// Put stores value under key in the current manager-owned object store.
func (b *ManagedBackend) Put(ctx context.Context, key string, value []byte) error {
	store, err := b.providerStore()
	if err != nil {
		return err
	}
	return store.Put(ctx, key, value)
}

// Get retrieves key from the current manager-owned object store.
func (b *ManagedBackend) Get(ctx context.Context, key string) ([]byte, error) {
	store, err := b.providerStore()
	if err != nil {
		return nil, err
	}
	return store.Get(ctx, key)
}

func (b *ManagedBackend) exactClaims(ctx context.Context) (jetstream.KeyValue, error) {
	b.claimsMu.Lock()
	defer b.claimsMu.Unlock()
	if b.claims != nil {
		return b.claims, nil
	}
	claims, err := openExactClaims(ctx, b.client, b.physicalBucket)
	if err != nil {
		return nil, err
	}
	b.claims = claims
	return claims, nil
}

// ClaimExact atomically establishes or returns the immutable value for key.
func (b *ManagedBackend) ClaimExact(ctx context.Context, key string, candidate []byte) ([]byte, error) {
	if len(candidate) > MaxCompanionDecisionBytes {
		return nil, fmt.Errorf("immutable companion decision is %d bytes; limit is %d",
			len(candidate), MaxCompanionDecisionBytes)
	}
	claims, err := b.exactClaims(ctx)
	if err != nil {
		return nil, err
	}
	var winner []byte
	if _, err := claims.Create(ctx, key, candidate); err == nil {
		winner = bytes.Clone(candidate)
	} else if !errors.Is(err, jetstream.ErrKeyExists) {
		return nil, fmt.Errorf("create exact-resident claim %q: %w", key, err)
	} else {
		entry, readErr := claims.Get(ctx, key)
		if readErr != nil {
			return nil, fmt.Errorf("read exact-resident claim %q: %w", key, readErr)
		}
		winner = bytes.Clone(entry.Value())
	}
	resident, err := b.Get(ctx, key)
	if errors.Is(err, storage.ErrObjectNotFound) {
		if err := b.Put(ctx, key, winner); err != nil {
			return nil, fmt.Errorf("repair exact-resident object %q: %w", key, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read exact-resident object %q: %w", key, err)
	} else if !bytes.Equal(resident, winner) {
		return nil, fmt.Errorf("%w: object %q differs from its immutable claim", ErrArtifactCorrupt, key)
	}
	return winner, nil
}
