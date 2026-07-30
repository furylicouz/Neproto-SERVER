package main

import (
	"bytes"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/geodata"
)

func (api *webAPI) writeGeoData(writer http.ResponseWriter) {
	status, err := geodata.Status(api.manager.GeodataDirectory())
	if err != nil {
		status.State = geodata.UpdateStateError
		status.Error = "GeoData is unavailable"
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{
		"status": status, "schedule": geoDataSchedule(api.manager.RootDirectory()),
	})
}

func (api *webAPI) startGeoDataUpdate(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Cluster bool `json:"cluster"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	api.startJob(writer, "geodata_update", func(report func(int, string)) (any, error) {
		var output, failures bytes.Buffer
		statuses, err := runGeoDataOperation(api.manager, api.controller, cluster.GeoDataUpdate, input.Cluster, report, &output, &failures)
		if err != nil {
			return nil, err
		}
		return map[string]any{"nodes": statuses}, nil
	})
}

func (api *webAPI) setGeoDataSchedule(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Preset string `json:"preset"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	if err := setGeoDataSchedule(api.manager.RootDirectory(), input.Preset); err != nil {
		api.writeOperationError(writer, err)
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "preset": input.Preset})
}

func (api *webAPI) runWebDoctor(writer http.ResponseWriter, request *http.Request) {
	var input struct{}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	var output, failures limitedBuffer
	output.maximum = webAPIMaximumLogBytes
	failures.maximum = webAPIMaximumLogBytes
	code := runDoctor(api.manager, api.controller, &output, &failures)
	lines := splitTUIOutput(output.String()+failures.String(), 400)
	if code != 0 {
		// The diagnostic command completed successfully even when one or more
		// checks failed. Return its structured report to the dashboard instead
		// of turning the report into a transport error that hides the details.
		api.writeJSON(writer, http.StatusOK, map[string]any{"ok": false, "lines": lines})
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "lines": lines})
}

func (api *webAPI) startDomainChange(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Domain  string `json:"domain"`
		Confirm string `json:"confirm"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	if input.Confirm != "CHANGE DOMAIN" || len(input.Domain) == 0 || len(input.Domain) > 253 {
		api.writeError(writer, http.StatusBadRequest, "confirmation_required", "type CHANGE DOMAIN to replace the authenticated server identity")
		return
	}
	domain := input.Domain
	api.startJob(writer, "domain_change", func(report func(int, string)) (any, error) {
		report(10, "Resolving the new public domain")
		addresses, err := resolveDomainAddresses(domain)
		if err != nil {
			return nil, err
		}
		report(30, "Creating rollback snapshot")
		var output, failures bytes.Buffer
		if code := performDomainSet(api.manager, api.controller, domain, addresses, &output, &failures); code != 0 {
			message := strings.TrimSpace(failures.String())
			if message == "" {
				message = "domain change failed and rollback was attempted"
			}
			return nil, errors.New(boundedDisplay(message, 480))
		}
		report(95, "Public readiness verified")
		return map[string]any{"domain": domain, "server_addresses": addresses}, nil
	})
}

func (api *webAPI) startBackupRestore(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		ID      string `json:"id"`
		Confirm string `json:"confirm"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	if input.Confirm != "RESTORE" || input.ID == "" || input.ID != filepath.Base(input.ID) || len(input.ID) > 128 {
		api.writeError(writer, http.StatusBadRequest, "confirmation_required", "type RESTORE and select a valid recovery snapshot")
		return
	}
	path := filepath.Join(api.manager.RootDirectory(), "var", "backups", "neproto", input.ID)
	api.startJob(writer, "backup_restore", func(report func(int, string)) (any, error) {
		report(10, "Validating selected recovery snapshot")
		var output, failures bytes.Buffer
		if code := backupCommand(api.manager, api.controller, []string{"restore", "--path", path, "--confirm", "RESTORE"}, &output, &failures); code != 0 {
			message := strings.TrimSpace(failures.String())
			if message == "" {
				message = "restore failed and recovery rollback was attempted"
			}
			return nil, errors.New(boundedDisplay(message, 480))
		}
		report(95, "Restored services passed readiness checks")
		return map[string]string{"restored": input.ID}, nil
	})
}
