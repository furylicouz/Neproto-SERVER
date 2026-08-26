package windowsclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"

	"neproto.local/chameleon/internal/cluster"
)

type API struct {
	controller *Controller
	store      *Store
	sequence   atomic.Int64
}

func NewAPI(controller *Controller, store *Store) *API {
	return &API{controller: controller, store: store}
}

func (a *API) Handle(request Request) Response {
	if a == nil || a.controller == nil || a.store == nil {
		return failedResponse(request.ID, errors.New("NeProto service unavailable"))
	}
	var result any
	var err error
	if isHostV1Method(request.Method) {
		return a.handleHostV1(request)
	}
	switch request.Method {
	case MethodStatus:
		err = decodeParams(request.Params, &struct{}{})
		if err == nil {
			result = a.controller.Status()
		}
	case MethodProfilesList:
		err = decodeParams(request.Params, &struct{}{})
		if err == nil {
			result = struct {
				Profiles []Profile `json:"profiles"`
				Selected string    `json:"selected_profile_id,omitempty"`
			}{a.controller.Profiles(), a.store.SelectedProfileID()}
		}
	case MethodProfilesImport:
		var params struct {
			URI string `json:"uri"`
		}
		err = decodeParams(request.Params, &params)
		if err == nil {
			result, err = a.controller.ImportProfile(params.URI)
		}
	case MethodProfilesRemove, MethodProfilesSelect:
		var params struct {
			ID string `json:"id"`
		}
		err = decodeParams(request.Params, &params)
		if err == nil && params.ID == "" {
			err = ErrProfileNotFound
		}
		if err == nil {
			if request.Method == MethodProfilesRemove {
				err = a.controller.RemoveProfile(params.ID)
			} else {
				err = a.controller.SelectProfile(params.ID)
			}
		}
	case MethodTunnelConnect:
		err = decodeParams(request.Params, &struct{}{})
		if err == nil {
			err = a.controller.Connect()
		}
		if err == nil {
			result = map[string]bool{"accepted": true}
		}
	case MethodTunnelDisconnect:
		err = decodeParams(request.Params, &struct{}{})
		if err == nil {
			err = a.controller.Disconnect()
		}
		if err == nil {
			result = map[string]bool{"accepted": true}
		}
	case MethodLogsTail:
		var params struct {
			Limit int `json:"limit,omitempty"`
		}
		err = decodeParams(request.Params, &params)
		if err == nil {
			result = a.controller.Logs(params.Limit)
		}
	case MethodCatalogSync:
		err = decodeParams(request.Params, &struct{}{})
		if err == nil {
			result, err = a.controller.SyncCatalog()
		}
	case MethodRoutesList:
		err = decodeParams(request.Params, &struct{}{})
		if err == nil {
			result = a.controller.Routes()
		}
	case MethodRoutesUpsert:
		var params struct {
			Route cluster.Route `json:"route"`
		}
		err = decodeParams(request.Params, &params)
		if err == nil {
			err = a.controller.UpsertLocalRoute(params.Route)
		}
		if err == nil {
			result = map[string]bool{"saved": true}
		}
	case MethodRoutesRemove:
		var params struct {
			ID string `json:"id"`
		}
		err = decodeParams(request.Params, &params)
		if err == nil {
			err = a.controller.RemoveLocalRoute(params.ID)
		}
		if err == nil {
			result = map[string]bool{"removed": true}
		}
	default:
		err = ErrInvalidIPCMessage
	}
	if err != nil {
		return failedResponse(request.ID, err)
	}
	return Response{ID: request.ID, OK: true, Result: result}
}

func failedResponse(id string, err error) Response {
	return Response{ID: id, OK: false, Error: safeError(err)}
}

func decodeParams(raw json.RawMessage, destination any) error {
	if len(raw) == 0 || len(raw) > MaxIPCMessageBytes || raw[0] != '{' {
		return ErrInvalidIPCMessage
	}
	if err := rejectDuplicateTopLevelFields(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrInvalidIPCMessage
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidIPCMessage
	}
	return nil
}
