package main

import (
	"net/http"
	"strings"

	"neproto.local/chameleon/internal/admin"
)

func (api *webAPI) handleRoute(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/v1/routes/"), "/")
	if len(parts) == 1 && request.Method == http.MethodDelete {
		api.deleteRoute(writer, request, parts[0])
		return
	}
	if len(parts) != 2 || request.Method != http.MethodPost || parts[0] == "" {
		api.writeError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	switch parts[1] {
	case "enable":
		var input struct {
			Enabled bool `json:"enabled"`
		}
		if !api.decodeJSON(writer, request, &input) {
			return
		}
		api.mutation.Lock()
		defer api.mutation.Unlock()
		if err := api.setRouteEnabled(parts[0], input.Enabled); err != nil {
			api.writeOperationError(writer, err)
			return
		}
		api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "id": parts[0], "enabled": input.Enabled})
	case "assign-user":
		var input struct {
			UserID  string `json:"user_id"`
			Enabled bool   `json:"enabled"`
		}
		if !api.decodeJSON(writer, request, &input) {
			return
		}
		api.mutation.Lock()
		defer api.mutation.Unlock()
		if err := api.setRouteForUser(parts[0], input.UserID, input.Enabled); err != nil {
			api.writeOperationError(writer, err)
			return
		}
		api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "route_id": parts[0], "user_id": input.UserID, "enabled": input.Enabled})
	default:
		api.writeError(writer, http.StatusNotFound, "not_found", "resource not found")
	}
}

func (api *webAPI) setRouteEnabled(routeID string, enabled bool) error {
	state, err := api.manager.ClusterState()
	if err != nil {
		return err
	}
	for _, route := range state.Routes {
		if route.ID == routeID {
			route.Enabled = enabled
			_, err = api.manager.UpsertClusterRoute(route)
			return err
		}
	}
	return admin.ErrClusterRouteNotFound
}

func (api *webAPI) setRouteForUser(routeID, userID string, enabled bool) error {
	state, err := api.manager.ClusterState()
	if err != nil {
		return err
	}
	currentlyEnabled := false
	var currentAccessIndex = -1
	for index, access := range state.Access {
		if access.UserID == userID {
			currentAccessIndex = index
			currentlyEnabled = containsString(access.AllowedRouteIDs, routeID)
			break
		}
	}
	if currentlyEnabled == enabled {
		return nil
	}
	if enabled {
		return assignRouteToUserSynced(api.manager, routeID, userID, api.syncCredentials)
	}
	if currentAccessIndex < 0 {
		return nil
	}
	access := state.Access[currentAccessIndex]
	filtered := make([]string, 0, len(access.AllowedRouteIDs))
	for _, allowed := range access.AllowedRouteIDs {
		if allowed != routeID {
			filtered = append(filtered, allowed)
		}
	}
	access.AllowedRouteIDs = filtered
	if err := api.syncCredentials(api.manager, userID, access.AllowedNodeIDs); err != nil {
		return err
	}
	_, err = api.manager.SetClusterUserAccess(access)
	return err
}

func (api *webAPI) deleteRoute(writer http.ResponseWriter, request *http.Request, routeID string) {
	var input struct {
		Confirm string `json:"confirm"`
	}
	if !api.decodeJSON(writer, request, &input) {
		return
	}
	if input.Confirm != "DELETE" {
		api.writeError(writer, http.StatusBadRequest, "confirmation_required", "type DELETE to remove the route")
		return
	}
	api.mutation.Lock()
	defer api.mutation.Unlock()
	if _, err := api.manager.RemoveClusterRoute(routeID); err != nil {
		api.writeOperationError(writer, err)
		return
	}
	api.writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "id": routeID})
}
