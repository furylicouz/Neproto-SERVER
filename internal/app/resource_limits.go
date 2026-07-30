package app

import (
	"neproto.local/chameleon/internal/config"
	"neproto.local/chameleon/internal/proxy"
)

func newProductionResourceLimiter(server config.Server) (*proxy.ResourceLimiter, error) {
	limits := server.ResourceLimits
	return proxy.NewResourceLimiter(proxy.ResourceLimitConfig{
		MaxSessionsPerUser:            limits.MaxSessionsPerUser,
		MaxTCPConnectionsGlobal:       limits.MaxTCPConnectionsGlobal,
		MaxTCPConnectionsPerUser:      limits.MaxTCPConnectionsPerUser,
		MaxUDPAssociationsGlobal:      limits.MaxUDPAssociationsGlobal,
		MaxUDPAssociationsPerUser:     limits.MaxUDPAssociationsPerUser,
		UDPPacketsPerSecondGlobal:     limits.UDPPacketsPerSecondGlobal,
		UDPPacketsPerSecondPerUser:    limits.UDPPacketsPerSecondPerUser,
		UDPBytesPerSecondGlobal:       limits.UDPBytesPerSecondGlobal,
		UDPBytesPerSecondPerUser:      limits.UDPBytesPerSecondPerUser,
		DNSQueriesPerSecondGlobal:     limits.DNSQueriesPerSecondGlobal,
		DNSQueriesPerSecondPerUser:    limits.DNSQueriesPerSecondPerUser,
		TargetCreatesPerSecondGlobal:  limits.TargetCreatesPerSecondGlobal,
		TargetCreatesPerSecondPerUser: limits.TargetCreatesPerSecondPerUser,
	})
}
