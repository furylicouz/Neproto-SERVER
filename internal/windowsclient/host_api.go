package windowsclient

import (
	"errors"
	"fmt"
	"strings"

	"neproto.local/chameleon/internal/buildinfo"
	"neproto.local/chameleon/internal/clienthost"
)

type hostCapabilities struct {
	APIVersion                hostAPIVersion `json:"api_version"`
	Platform                  string         `json:"platform"`
	AppVersion                string         `json:"app_version"`
	HostVersion               string         `json:"host_version"`
	CoreVersion               string         `json:"core_version"`
	SupportsHTTP3WebTransport bool           `json:"supports_http3_web_transport"`
}

type hostAPIVersion struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

type hostProfileSummary struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name"`
	ServerIdentity  string `json:"server_identity"`
	Host            string `json:"host"`
	Selected        bool   `json:"selected"`
	HasCredential   bool   `json:"has_credential"`
	Origin          string `json:"origin"`
	CatalogManaged  bool   `json:"catalog_managed"`
	UpdatedAtUnixMS int64  `json:"updated_at_unix_ms"`
}

type hostDiagnostics struct {
	AppVersion     string                `json:"app_version"`
	HostVersion    string                `json:"host_version"`
	CoreVersion    string                `json:"core_version"`
	CarrierPolicy  string                `json:"carrier_policy"`
	CurrentCarrier clienthost.Carrier    `json:"current_carrier"`
	ReconnectCount int64                 `json:"reconnect_count"`
	Events         []hostDiagnosticEvent `json:"events"`
}

type hostDiagnosticEvent struct {
	UnixMS      int64            `json:"unix_ms"`
	Level       string           `json:"level"`
	Stage       clienthost.Stage `json:"stage"`
	Code        *clienthost.Code `json:"code,omitempty"`
	Message     string           `json:"message"`
	OperationID string           `json:"operation_id"`
	Sequence    int64            `json:"sequence"`
}

func isHostV1Method(method Method) bool {
	switch method {
	case MethodHostV1Capabilities, MethodHostV1ProfilesList, MethodHostV1ProfilesImport,
		MethodHostV1ProfilesSelect, MethodHostV1ProfilesRemove, MethodHostV1TunnelConnect,
		MethodHostV1TunnelDisconnect, MethodHostV1Status, MethodHostV1Diagnostics:
		return true
	default:
		return false
	}
}

func (a *API) handleHostV1(request Request) Response {
	if a == nil || a.controller == nil || a.store == nil {
		return hostFailure(request.ID, "host-api", clienthost.StageHostIPC,
			errors.New("native host unavailable"))
	}
	switch request.Method {
	case MethodHostV1Capabilities:
		var params struct {
			Major int `json:"api_major"`
			Minor int `json:"api_minor"`
		}
		if err := decodeParams(request.Params, &params); err != nil {
			return hostFailure(request.ID, "capabilities", clienthost.StageHostNegotiation, err)
		}
		if params.Major != clienthost.HostAPIMajor || params.Minor < 0 || params.Minor > clienthost.HostAPIMinor {
			return hostFailure(request.ID, "capabilities", clienthost.StageHostNegotiation,
				errors.New("unsupported Host API version"))
		}
		return hostSuccess(request.ID, hostCapabilities{
			APIVersion: hostAPIVersion{Major: clienthost.HostAPIMajor, Minor: clienthost.HostAPIMinor},
			Platform:   "windows", AppVersion: buildinfo.Version, HostVersion: buildinfo.Version,
			CoreVersion: buildinfo.Version, SupportsHTTP3WebTransport: true,
		})
	case MethodHostV1ProfilesList:
		if err := decodeParams(request.Params, &struct{}{}); err != nil {
			return hostFailure(request.ID, "profiles-list", clienthost.StageProfileValidation, err)
		}
		return hostSuccess(request.ID, map[string]any{"profiles": a.hostProfiles()})
	case MethodHostV1ProfilesImport:
		var params struct {
			OnboardingValue string `json:"onboarding_value"`
			OperationID     string `json:"operation_id"`
		}
		if err := decodeParams(request.Params, &params); err != nil ||
			clienthost.ValidateOnboardingValue(params.OnboardingValue) != nil ||
			clienthost.ValidateOperationID(params.OperationID) != nil {
			return hostFailure(request.ID, "invalid-operation", clienthost.StageProfileValidation,
				clienthost.ErrInvalidInput)
		}
		profile, err := a.controller.ImportProfile(params.OnboardingValue)
		if err != nil {
			return hostFailure(request.ID, params.OperationID, clienthost.StageProfileValidation, err)
		}
		return hostSuccess(request.ID, a.hostProfile(profile))
	case MethodHostV1ProfilesSelect:
		var params hostProfileOperation
		if response := decodeHostProfileOperation(request, &params); response != nil {
			return *response
		}
		if err := a.controller.SelectProfile(params.ProfileID); err != nil {
			return hostFailure(request.ID, params.OperationID, clienthost.StageProfileValidation, err)
		}
		profile, ok := a.profileByID(params.ProfileID)
		if !ok {
			return hostFailure(request.ID, params.OperationID, clienthost.StageProfileValidation, ErrProfileNotFound)
		}
		return hostSuccess(request.ID, a.hostProfile(profile))
	case MethodHostV1ProfilesRemove:
		var params struct {
			ProfileID   string `json:"profile_id"`
			Force       bool   `json:"force"`
			OperationID string `json:"operation_id"`
		}
		if err := decodeParams(request.Params, &params); err != nil ||
			clienthost.ValidateProfileID(params.ProfileID) != nil ||
			clienthost.ValidateOperationID(params.OperationID) != nil {
			return hostFailure(request.ID, "invalid-operation", clienthost.StageProfileValidation,
				clienthost.ErrInvalidInput)
		}
		if err := a.controller.RemoveProfile(params.ProfileID); err != nil {
			return hostFailure(request.ID, params.OperationID, clienthost.StageProfileValidation, err)
		}
		return hostSuccess(request.ID, map[string]bool{"removed": true})
	case MethodHostV1TunnelConnect:
		var params hostProfileOperation
		if response := decodeHostProfileOperation(request, &params); response != nil {
			return *response
		}
		if err := a.controller.SelectProfile(params.ProfileID); err != nil {
			return hostFailure(request.ID, params.OperationID, clienthost.StageProfileValidation, err)
		}
		if err := a.controller.Connect(); err != nil {
			return hostFailure(request.ID, params.OperationID, clienthost.StageUnknown, err)
		}
		return hostSuccess(request.ID, a.hostStatus())
	case MethodHostV1TunnelDisconnect:
		var params struct {
			OperationID string `json:"operation_id"`
		}
		if err := decodeParams(request.Params, &params); err != nil ||
			clienthost.ValidateOperationID(params.OperationID) != nil {
			return hostFailure(request.ID, "invalid-operation", clienthost.StageHostIPC,
				clienthost.ErrInvalidInput)
		}
		if err := a.controller.Disconnect(); err != nil {
			return hostFailure(request.ID, params.OperationID, clienthost.StageUnknown, err)
		}
		return hostSuccess(request.ID, a.hostStatus())
	case MethodHostV1Status:
		if err := decodeParams(request.Params, &struct{}{}); err != nil {
			return hostFailure(request.ID, "status", clienthost.StageHostIPC, err)
		}
		return hostSuccess(request.ID, a.hostStatus())
	case MethodHostV1Diagnostics:
		var params struct {
			Limit int `json:"limit"`
		}
		if err := decodeParams(request.Params, &params); err != nil ||
			clienthost.ValidateDiagnosticsLimit(params.Limit) != nil {
			return hostFailure(request.ID, "diagnostics", clienthost.StageHostIPC,
				clienthost.ErrInvalidInput)
		}
		return hostSuccess(request.ID, a.hostDiagnostics(params.Limit))
	default:
		return hostFailure(request.ID, "host-api", clienthost.StageHostIPC, ErrInvalidIPCMessage)
	}
}

type hostProfileOperation struct {
	ProfileID   string `json:"profile_id"`
	OperationID string `json:"operation_id"`
}

func decodeHostProfileOperation(request Request, params *hostProfileOperation) *Response {
	if err := decodeParams(request.Params, params); err != nil ||
		clienthost.ValidateProfileID(params.ProfileID) != nil ||
		clienthost.ValidateOperationID(params.OperationID) != nil {
		response := hostFailure(request.ID, "invalid-operation", clienthost.StageProfileValidation,
			clienthost.ErrInvalidInput)
		return &response
	}
	return nil
}

func (a *API) hostProfiles() []hostProfileSummary {
	profiles := a.controller.Profiles()
	result := make([]hostProfileSummary, 0, len(profiles))
	for _, profile := range profiles {
		result = append(result, a.hostProfile(profile))
	}
	return result
}

func (a *API) hostProfile(profile Profile) hostProfileSummary {
	origin := "imported"
	if profile.ManagedByCluster {
		origin = "cluster"
	}
	return hostProfileSummary{
		ID: profile.ID, DisplayName: profile.Name, ServerIdentity: profile.ServerIdentity,
		Host: profile.ServerIdentity, Selected: profile.ID == a.store.SelectedProfileID(),
		HasCredential: true, Origin: origin, CatalogManaged: profile.ManagedByCluster,
	}
}

func (a *API) profileByID(id string) (Profile, bool) {
	for _, profile := range a.controller.Profiles() {
		if profile.ID == id {
			return profile, true
		}
	}
	return Profile{}, false
}

func (a *API) hostStatus() clienthost.Snapshot {
	status := a.controller.Status()
	state := clienthost.StateUnknown
	carrier := clienthost.CarrierUnknown
	switch status.State {
	case StateStopped:
		state, carrier = clienthost.StateDisconnected, clienthost.CarrierNone
	case StateConnecting:
		state, carrier = clienthost.StateConnecting, clienthost.CarrierNone
	case StateConnected:
		state = clienthost.StateConnected
		if status.Carrier == "http3" || status.Carrier == string(clienthost.CarrierHTTP3WebTransport) {
			carrier = clienthost.CarrierHTTP3WebTransport
		}
	case StateDisconnecting:
		state = clienthost.StateDisconnecting
		carrier = clienthost.CarrierHTTP3WebTransport
	case StateFailed:
		state, carrier = clienthost.StateFailed, clienthost.CarrierNone
	}
	connectedAt := int64(0)
	if !status.ConnectedSince.IsZero() {
		connectedAt = status.ConnectedSince.UnixMilli()
	}
	snapshot := clienthost.Snapshot{
		State: state, ProfileID: status.SelectedProfileID, Carrier: carrier,
		ConnectedAtUnixMS: connectedAt, UploadBytesPerSecond: status.UploadBytesPerSecond,
		DownloadBytesPerSecond: status.DownloadBytesPerSecond, UploadTotalBytes: status.UploadTotalBytes,
		DownloadTotalBytes: status.DownloadTotalBytes, Sequence: a.sequence.Add(1),
	}
	if status.LastError != "" {
		public := clienthost.MapError("status", clienthost.StageUnknown, errors.New(status.LastError))
		snapshot.LastError = &public
	}
	return snapshot
}

func (a *API) hostDiagnostics(limit int) hostDiagnostics {
	logs := a.controller.Logs(limit)
	events := make([]hostDiagnosticEvent, 0, len(logs))
	for index, entry := range logs {
		message := safeError(errors.New(entry.Message))
		if clienthost.ValidateDiagnosticMessage(message) != nil || strings.Contains(message, "np2://") {
			message = "Operation failed."
		}
		level := strings.ToLower(entry.Level)
		if level != "info" && level != "warning" && level != "error" {
			level = "unknown"
		}
		events = append(events, hostDiagnosticEvent{
			UnixMS: entry.Time.UnixMilli(), Level: level, Stage: clienthost.StageUnknown,
			Message: message, OperationID: fmt.Sprintf("diagnostic-%d", index+1), Sequence: int64(index + 1),
		})
	}
	return hostDiagnostics{
		AppVersion: buildinfo.Version, HostVersion: buildinfo.Version, CoreVersion: buildinfo.Version,
		CarrierPolicy: "http3-only", CurrentCarrier: a.hostStatus().Carrier,
		ReconnectCount: 0, Events: events,
	}
}

func hostSuccess(id string, result any) Response {
	return Response{ID: id, OK: true, Result: result}
}

func hostFailure(id, operationID string, stage clienthost.Stage, err error) Response {
	public := clienthost.MapError(operationID, stage, err)
	return Response{ID: id, OK: false, Error: public.Message, HostError: &public}
}
