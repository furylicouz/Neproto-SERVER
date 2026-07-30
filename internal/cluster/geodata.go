package cluster

import (
	"errors"
	"time"
)

const GeoDataControlVersion = 1

type GeoDataOperation string

const (
	GeoDataStatus GeoDataOperation = "status"
	GeoDataUpdate GeoDataOperation = "update"
)

type GeoDataRequest struct {
	Version   int              `json:"version"`
	Operation GeoDataOperation `json:"operation"`
}

type GeoDataNodeStatus struct {
	Version       int       `json:"version"`
	NodeID        string    `json:"node_id"`
	State         string    `json:"state"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
	GeoIPSHA256   string    `json:"geoip_sha256,omitempty"`
	GeoSiteSHA256 string    `json:"geosite_sha256,omitempty"`
	GeoIPBytes    int64     `json:"geoip_bytes,omitempty"`
	GeoSiteBytes  int64     `json:"geosite_bytes,omitempty"`
	Error         string    `json:"error,omitempty"`
}

func ValidateGeoDataRequest(request GeoDataRequest) error {
	if request.Version != GeoDataControlVersion ||
		(request.Operation != GeoDataStatus && request.Operation != GeoDataUpdate) {
		return errors.New("invalid cluster geodata request")
	}
	return nil
}
