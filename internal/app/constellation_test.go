package app

import (
	"context"
	"testing"

	"neproto.local/chameleon/internal/config"
)

func TestConstellationServicesAreDisabledByDefaultAndBoundedWhenEnabled(t *testing.T) {
	disabled := config.Server{MaxSessions: 4, MaxTargetConnections: 8}
	if services, err := newConstellationServices(context.Background(), disabled); err != nil || services != nil {
		t.Fatalf("disabled services=%+v err=%v", services, err)
	}
	enabled := disabled
	enabled.EnableConstellation = true
	services, err := newConstellationServices(context.Background(), enabled)
	if err != nil {
		t.Fatal(err)
	}
	if services == nil || services.hub == nil || services.control == nil || services.runtime == nil {
		t.Fatalf("enabled services=%+v", services)
	}
	if err := services.Close(); err != nil {
		t.Fatal(err)
	}
	if err := services.Close(); err != nil {
		t.Fatal(err)
	}
}
