package app

import (
	"context"
	"errors"
	"time"

	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/constellation"
	"neproto.local/chameleon/internal/continuity"
	"neproto.local/chameleon/internal/proxy"
)

const (
	productionConstellationMaxFlows      = 128
	productionConstellationFlowsPerUser  = 64
	productionConstellationMaxLeases     = 3
	productionConstellationJournalBytes  = 256 * 1024
	productionConstellationAckEveryBytes = 64 * 1024
	productionConstellationMigration     = 15 * time.Second
	productionConstellationTicketTTL     = time.Minute
	productionConstellationInactiveTTL   = 5 * time.Minute
)

type constellationServices struct {
	hub     *constellation.Hub
	control *constellation.ServerControl
	runtime *proxy.ContinuityRuntime
}

func newConstellationServices(
	ctx context.Context,
	serverConfig config.Server,
) (*constellationServices, error) {
	if !serverConfig.EnableConstellation {
		return nil, nil
	}
	if ctx == nil || serverConfig.MaxSessions <= 0 || serverConfig.MaxTargetConnections <= 0 {
		return nil, config.ErrInvalidConfig
	}
	maxConstellations := min(serverConfig.MaxSessions, constellation.MaxConstellations)
	maxTickets := maxConstellations * productionConstellationMaxLeases
	if maxTickets > continuity.MaxLeaseTickets {
		maxTickets = continuity.MaxLeaseTickets
	}
	hub, err := constellation.NewHub(constellation.HubConfig{
		MaxConstellations: maxConstellations,
		MaxLeases:         productionConstellationMaxLeases, MaxDraining: productionConstellationMaxLeases,
		InactiveTTL: productionConstellationInactiveTTL,
		TicketConfig: continuity.TicketRegistryConfig{
			MaxTickets: maxTickets, TTL: productionConstellationTicketTTL,
		},
	})
	if err != nil {
		return nil, err
	}
	control, err := constellation.NewServerControl(constellation.ServerControlConfig{Hub: hub})
	if err != nil {
		_ = hub.Close()
		return nil, err
	}
	maxFlows := min(serverConfig.MaxTargetConnections, productionConstellationMaxFlows)
	maxFlowsPerUser := min(maxFlows, productionConstellationFlowsPerUser)
	runtime, err := proxy.NewContinuityRuntime(proxy.ContinuityRuntimeConfig{
		Context: ctx, MaxFlows: maxFlows, MaxFlowsPerPrincipal: maxFlowsPerUser,
		JournalBytes:     productionConstellationJournalBytes,
		AckEveryBytes:    productionConstellationAckEveryBytes,
		MigrationTimeout: productionConstellationMigration,
	})
	if err != nil {
		_ = hub.Close()
		return nil, err
	}
	return &constellationServices{hub: hub, control: control, runtime: runtime}, nil
}

func (s *constellationServices) Close() error {
	if s == nil {
		return nil
	}
	return errors.Join(s.runtime.Close(), s.hub.Close())
}
