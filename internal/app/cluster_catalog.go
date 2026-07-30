package app

import (
	"context"
	"fmt"
	"time"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/proxy"
)

func newClusterCatalogHandler(directory string, ttl time.Duration, now func() time.Time) (proxy.CatalogHandler, error) {
	if directory == "" {
		return nil, nil
	}
	if ttl < 5*time.Minute || ttl > cluster.MaxCatalogTTL {
		return nil, fmt.Errorf("invalid cluster catalog TTL")
	}
	if now == nil {
		now = time.Now
	}
	store, err := cluster.OpenStore(directory)
	if err != nil {
		return nil, err
	}
	if _, _, err := store.LoadSigningKey(); err != nil {
		return nil, fmt.Errorf("load cluster catalog signing key: %w", err)
	}
	return func(_ context.Context, credentialID string) ([]byte, error) {
		state, err := store.Load()
		if err != nil {
			return nil, err
		}
		catalog, err := cluster.BuildCatalog(state, credentialID, now().UTC(), ttl)
		if err != nil {
			return nil, err
		}
		_, privateKey, err := store.LoadSigningKey()
		if err != nil {
			return nil, err
		}
		signed, err := cluster.SignCatalog(catalog, privateKey)
		if err != nil {
			return nil, err
		}
		raw, err := cluster.EncodeCatalogEnvelope(signed)
		if err != nil || len(raw) == 0 || len(raw) > proxy.MaxCatalogPayload {
			return nil, cluster.ErrInvalidCatalog
		}
		return raw, nil
	}, nil
}
