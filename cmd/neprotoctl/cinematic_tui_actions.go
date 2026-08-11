package main

import (
	"bytes"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/cluster"
	"neproto.local/chameleon/internal/onboarding"
)

func handleTUIDialogKey(event *tcell.EventKey, model *constellationTUIModel) (handled, invoke bool) {
	dialog := model.dialog
	if dialog == nil {
		return false, false
	}
	if event.Key() == tcell.KeyEscape || (dialog.kind == tuiDialogInfo && event.Key() == tcell.KeyEnter) {
		if isClusterEnrollmentOperation(dialog.operation) {
			model.clearClusterDraft()
		}
		if isRouteDraftOperation(dialog.operation) {
			model.routeDraft = clusterRouteDraft{}
		}
		model.dialog = nil
		model.status = "WORKSPACE READY"
		return true, false
	}
	if dialog.kind == tuiDialogInfo {
		switch event.Key() {
		case tcell.KeyUp:
			dialog.scroll = maxInt(0, dialog.scroll-1)
		case tcell.KeyDown:
			dialog.scroll = minInt(maxInt(0, len(dialog.lines)-1), dialog.scroll+1)
		case tcell.KeyPgUp:
			dialog.scroll = maxInt(0, dialog.scroll-10)
		case tcell.KeyPgDn:
			dialog.scroll = minInt(maxInt(0, len(dialog.lines)-1), dialog.scroll+10)
		case tcell.KeyHome:
			dialog.scroll = 0
		case tcell.KeyEnd:
			dialog.scroll = maxInt(0, len(dialog.lines)-1)
		}
		return true, false
	}
	if dialog.kind == tuiDialogSelect || dialog.kind == tuiDialogMultiSelect {
		if len(dialog.options) == 0 {
			return true, false
		}
		switch event.Key() {
		case tcell.KeyUp:
			dialog.optionIndex = (dialog.optionIndex - 1 + len(dialog.options)) % len(dialog.options)
			return true, false
		case tcell.KeyDown:
			dialog.optionIndex = (dialog.optionIndex + 1) % len(dialog.options)
			return true, false
		case tcell.KeyHome:
			dialog.optionIndex = 0
			return true, false
		case tcell.KeyEnd:
			dialog.optionIndex = len(dialog.options) - 1
			return true, false
		}
		if dialog.kind == tuiDialogMultiSelect && event.Key() == tcell.KeyRune {
			switch unicode.ToLower(event.Rune()) {
			case ' ':
				dialog.options[dialog.optionIndex].selected = !dialog.options[dialog.optionIndex].selected
				return true, false
			case 'a':
				selectAll := false
				for _, option := range dialog.options {
					selectAll = selectAll || !option.selected
				}
				for index := range dialog.options {
					dialog.options[index].selected = selectAll
				}
				return true, false
			}
		}
		if event.Key() == tcell.KeyEnter {
			value := dialog.options[dialog.optionIndex].value
			if dialog.kind == tuiDialogMultiSelect {
				selected := make([]string, 0, len(dialog.options))
				for _, option := range dialog.options {
					if option.selected {
						selected = append(selected, option.value)
					}
				}
				if len(selected) == 0 {
					dialog.prompt = "SELECT AT LEAST ONE USER WITH SPACE"
					return true, false
				}
				value = strings.Join(selected, ",")
			}
			if handled := advanceRouteDialog(model, dialog, value); handled {
				return true, model.pending.operation != tuiOperationNone
			}
			model.pending = tuiPendingOperation{operation: dialog.operation, value: value, aux: dialog.aux}
			model.dialog = nil
			return true, true
		}
		return true, false
	}
	if dialog.kind == tuiDialogConfirm {
		if event.Key() == tcell.KeyEnter {
			model.pending = tuiPendingOperation{operation: dialog.operation, value: dialog.value, aux: dialog.aux}
			model.dialog = nil
			return true, true
		}
		return true, false
	}
	if event.Key() == tcell.KeyBackspace || event.Key() == tcell.KeyBackspace2 {
		if dialog.secret {
			if len(dialog.secretRunes) > 0 {
				dialog.secretRunes[len(dialog.secretRunes)-1] = 0
				dialog.secretRunes = dialog.secretRunes[:len(dialog.secretRunes)-1]
			}
			return true, false
		}
		runes := []rune(dialog.input)
		if len(runes) > 0 {
			dialog.input = string(runes[:len(runes)-1])
		}
		return true, false
	}
	if event.Key() == tcell.KeyRune && !unicode.IsControl(event.Rune()) && len([]rune(dialog.input)) < 253 {
		if dialog.secret {
			if len(dialog.secretRunes) < 253 {
				dialog.secretRunes = append(dialog.secretRunes, event.Rune())
			}
			return true, false
		}
		dialog.input += string(event.Rune())
		return true, false
	}
	if event.Key() == tcell.KeyEnter {
		if dialog.secret {
			if len(dialog.secretRunes) == 0 {
				dialog.prompt = "VALUE IS REQUIRED"
				return true, false
			}
			password := make([]byte, 0, len(dialog.secretRunes)*2)
			for _, character := range dialog.secretRunes {
				password = utf8.AppendRune(password, character)
			}
			for index := range model.clusterDraft.password {
				model.clusterDraft.password[index] = 0
			}
			model.clusterDraft.password = password
			for index := range dialog.secretRunes {
				dialog.secretRunes[index] = 0
			}
			dialog.secretRunes = nil
			model.dialog = clusterEnrollmentDialog(tuiOperationClusterEnrollNodeID, "NODE ID (EXAMPLE: edge-fi)", "")
			return true, false
		}
		value := strings.TrimSpace(dialog.input)
		if value == "" {
			dialog.prompt = "VALUE IS REQUIRED"
			return true, false
		}
		if dialog.operation == tuiOperationDomainSet {
			if err := validateInstallDomain(value); err != nil {
				dialog.prompt = strings.ToUpper(err.Error())
				return true, false
			}
		}
		if handled := advanceClusterEnrollmentDialog(model, dialog, value); handled {
			for index := range dialog.secretRunes {
				dialog.secretRunes[index] = 0
			}
			dialog.input = ""
			return true, model.pending.operation != tuiOperationNone
		}
		if handled := advanceRouteDialog(model, dialog, value); handled {
			return true, model.pending.operation != tuiOperationNone
		}
		model.pending = tuiPendingOperation{operation: dialog.operation, value: value, aux: dialog.aux}
		model.dialog = nil
		return true, true
	}
	return true, false
}

func handleTUIWorkspaceActionKey(event *tcell.EventKey, model *constellationTUIModel) (handled, invoke bool) {
	if event.Key() != tcell.KeyRune && event.Key() != tcell.KeyEnter {
		return false, false
	}
	runeKey := unicode.ToLower(event.Rune())
	switch model.view {
	case tuiViewUsers:
		switch runeKey {
		case 'n':
			model.dialog = &tuiDialog{kind: tuiDialogText, title: "CREATE NP/2 USER", prompt: "DEVICE OR USER NAME", operation: tuiOperationUserAdd, aux: "web"}
			return true, false
		case 'e':
			return queueSelectedUserOperation(model, tuiOperationUserExport, false)
		case 'o':
			return queueSelectedUserOperation(model, tuiOperationUserRotate, true)
		case 'r':
			return queueSelectedUserOperation(model, tuiOperationUserRevoke, true)
		case 'd':
			return queueSelectedUserOperation(model, tuiOperationUserDelete, true)
		case 'c':
			return queueSelectedUserClusterAccess(model)
		}
	case tuiViewService:
		switch runeKey {
		case '1':
			model.pending.operation = tuiOperationServiceStart
			return true, true
		case '2':
			model.dialog = confirmationDialog("STOP NP/2 SERVICES", "STOP", tuiOperationServiceStop, "")
			return true, false
		case '3':
			model.dialog = confirmationDialog("RESTART NP/2 SERVICES", "RESTART", tuiOperationServiceRestart, "")
			return true, false
		case '4':
			model.pending.operation = tuiOperationServiceValidate
			return true, true
		case '5':
			model.pending.operation = tuiOperationServiceLogs
			return true, true
		}
	case tuiViewDomain:
		switch runeKey {
		case 'd':
			model.dialog = &tuiDialog{kind: tuiDialogText, title: "CHANGE DOMAIN", prompt: "PUBLIC DNS NAME", operation: tuiOperationDomainSet}
			return true, false
		case 'p':
			model.dialog = confirmationDialog("ENABLE PRODUCTION POLICY", "PRODUCTION", tuiOperationFeatureProduction, "")
			return true, false
		case 'c':
			model.dialog = confirmationDialog("ENABLE COMPATIBILITY POLICY", "COMPATIBILITY", tuiOperationFeatureCompatibility, "")
			return true, false
		case 'v':
			model.pending.operation = tuiOperationServiceValidate
			return true, true
		}
	case tuiViewBackups:
		if runeKey == 'n' {
			model.pending.operation = tuiOperationBackupCreate
			return true, true
		}
		if event.Key() == tcell.KeyEnter && len(model.backups) > 0 {
			index := minInt(maxInt(0, model.listIndex), len(model.backups)-1)
			path := model.backups[index]
			model.dialog = confirmationDialog("RESTORE RECOVERY SNAPSHOT", "RESTORE", tuiOperationBackupRestore, path)
			return true, false
		}
	case tuiViewCluster:
		switch runeKey {
		case 'n':
			model.clearClusterDraft()
			model.dialog = clusterEnrollmentDialog(tuiOperationClusterEnrollHost, "SSH HOST OR IP", "")
			return true, false
		case 'e':
			return queueSelectedClusterNodeOperation(model, tuiOperationClusterToggleEnabled, "CHANGE NODE TRAFFIC STATE")
		case 'p':
			return queueSelectedClusterNodeOperation(model, tuiOperationClusterTogglePublished, "CHANGE CLIENT VISIBILITY")
		case 'a':
			if len(model.clusterNodes) == 0 {
				model.dialog = infoDialog("NO CLUSTER NODE SELECTED", []string{"Enrol a node before assigning it."})
				return true, false
			}
			index := minInt(maxInt(0, model.listIndex), len(model.clusterNodes)-1)
			node := model.clusterNodes[index]
			options := activeUserDialogOptions(model.users)
			if len(options) == 0 {
				model.dialog = infoDialog("NO ACTIVE USERS", []string{"Create an active user before assigning a cluster node."})
				return true, false
			}
			model.dialog = routeSelectDialog(tuiOperationClusterAssignUser, "SELECT USER FOR "+strings.ToUpper(node.Name), options, false)
			model.dialog.title = "ASSIGN CLUSTER NODE"
			model.dialog.aux = node.ID
			return true, false
		case 'd':
			return queueSelectedClusterNodeOperation(model, tuiOperationClusterRemove, "REMOVE CLUSTER NODE")
		case 's':
			model.pending.operation = tuiOperationClusterSyncUsers
			return true, true
		}
	case tuiViewRoutes:
		switch runeKey {
		case 'n':
			model.routeDraft = clusterRouteDraft{}
			model.dialog = routeDialog(tuiOperationRouteID, "ROUTE ID", "")
			return true, false
		case 'e':
			return queueSelectedRouteOperation(model, tuiOperationRouteToggleEnabled, "ENABLE OR DISABLE ROUTE")
		case 'd':
			return queueSelectedRouteOperation(model, tuiOperationRouteRemove, "DELETE ROUTE")
		case 'a':
			if len(model.clusterRoutes) == 0 {
				model.dialog = infoDialog("NO ROUTE SELECTED", []string{"Create a route before assigning it."})
				return true, false
			}
			index := minInt(maxInt(0, model.listIndex), len(model.clusterRoutes)-1)
			options := activeUserDialogOptions(model.users)
			if len(options) == 0 {
				model.dialog = infoDialog("NO ACTIVE USERS", []string{"Create an active user before assigning a route."})
				return true, false
			}
			model.dialog = routeSelectDialog(tuiOperationRouteAssignUser, "SELECT USER FOR "+strings.ToUpper(model.clusterRoutes[index].Name), options, false)
			model.dialog.title = "ASSIGN ROUTE TO USER"
			model.dialog.aux = model.clusterRoutes[index].ID
			return true, false
		case 'g':
			model.dialog = &tuiDialog{
				kind: tuiDialogConfirm, title: "UPDATE CLUSTER GEODATA",
				prompt:    "DOWNLOAD, VERIFY AND ACTIVATE GEOIP/GEOSITE ON EVERY NODE?",
				operation: tuiOperationGeoDataUpdate,
			}
			return true, false
		case 't':
			model.dialog = routeSelectDialog(tuiOperationGeoDataSchedule, "AUTOMATIC GEODATA UPDATE SCHEDULE", []tuiDialogOption{
				{label: "DAILY", value: "daily"},
				{label: "WEEKLY (RECOMMENDED)", value: "weekly"},
				{label: "MONTHLY", value: "monthly"},
				{label: "OFF", value: "off"},
			}, false)
			model.dialog.title = "GEODATA AUTOMATION"
			return true, false
		}
	}
	return false, false
}

func routeDialog(operation tuiOperation, prompt, initial string) *tuiDialog {
	return &tuiDialog{kind: tuiDialogText, title: "CREATE TRAFFIC ROUTE", prompt: prompt, input: initial, operation: operation}
}

func routeSelectDialog(operation tuiOperation, prompt string, options []tuiDialogOption, multiple bool) *tuiDialog {
	kind := tuiDialogSelect
	if multiple {
		kind = tuiDialogMultiSelect
	}
	return &tuiDialog{kind: kind, title: "CREATE TRAFFIC ROUTE", prompt: prompt, operation: operation, options: options}
}

func activeUserDialogOptions(users []admin.User) []tuiDialogOption {
	options := make([]tuiDialogOption, 0, len(users))
	for _, user := range users {
		if user.Status == admin.StatusActive {
			options = append(options, tuiDialogOption{label: user.Name + "  [automatic]", value: user.ID})
		}
	}
	return options
}

func advanceRouteDialog(model *constellationTUIModel, dialog *tuiDialog, value string) bool {
	if !isRouteDraftOperation(dialog.operation) {
		return false
	}
	draft := &model.routeDraft
	switch dialog.operation {
	case tuiOperationRouteID:
		draft.id = value
		model.dialog = routeDialog(tuiOperationRouteName, "DISPLAY NAME", "")
	case tuiOperationRouteName:
		draft.name = value
		model.dialog = routeDialog(tuiOperationRoutePriority, "PRIORITY (LOWER RUNS FIRST)", "100")
	case tuiOperationRoutePriority:
		priority, err := strconv.Atoi(value)
		if err != nil || priority < 0 || priority > 1_000_000 {
			dialog.prompt = "PRIORITY MUST BE 0-1000000"
			return true
		}
		draft.priority = priority
		model.dialog = routeSelectDialog(tuiOperationRouteMatch, "WHAT SHOULD THIS ROUTE MATCH?", []tuiDialogOption{
			{label: "DOMAIN OR SUBDOMAINS", value: "domain"},
			{label: "IP ADDRESS OR CIDR", value: "ip"},
			{label: "GEOIP COUNTRY", value: "geoip"},
			{label: "GEOSITE CATEGORY", value: "geosite"},
		}, false)
	case tuiOperationRouteMatch:
		draft.match = strings.ToLower(value) + ":"
		prompt := map[string]string{
			"domain":  "DOMAIN (EXAMPLE: youtube.com)",
			"ip":      "IP OR CANONICAL CIDR (EXAMPLE: 1.1.1.1 OR 1.1.1.0/24)",
			"geoip":   "COUNTRY CODE (EXAMPLE: NL, DE, US)",
			"geosite": "GEOSITE CATEGORY (EXAMPLE: youtube, telegram, category-media)",
		}[strings.ToLower(value)]
		model.dialog = routeDialog(tuiOperationRouteMatchValue, prompt, "")
	case tuiOperationRouteMatchValue:
		draft.match += strings.ToLower(value)
		options := []tuiDialogOption{
			{label: "KEEP ON CURRENT ENTRY SERVER", value: "current"},
			{label: "DIRECT FROM CURRENT SERVER", value: "direct"},
			{label: "AUTO SELECT ALLOWED SERVER", value: "auto"},
			{label: "BLOCK MATCHING TRAFFIC", value: "block"},
		}
		for _, node := range model.clusterNodes {
			if !node.Enabled {
				continue
			}
			options = append(options, tuiDialogOption{
				label: fmt.Sprintf("SEND VIA %s [%s]", node.Name, node.Region),
				value: "node:" + node.ID,
			})
		}
		model.dialog = routeSelectDialog(tuiOperationRouteAction, "WHERE SHOULD MATCHING TRAFFIC EXIT?", options, false)
	case tuiOperationRouteAction:
		draft.action = value
		users := activeUserDialogOptions(model.users)
		if len(users) == 0 {
			model.dialog = &tuiDialog{kind: tuiDialogInfo, title: "NO ACTIVE USERS", operation: tuiOperationRouteUsers, lines: []string{"Create an active user before assigning a route."}}
			return true
		}
		model.dialog = routeSelectDialog(tuiOperationRouteUsers, "WHICH USERS RECEIVE THIS ROUTE?", users, true)
	case tuiOperationRouteUsers:
		draft.userIDs = strings.Split(value, ",")
		model.dialog = confirmationDialog(
			"CREATE ROUTE // "+strings.ToUpper(draft.name),
			"CREATE",
			tuiOperationRouteCreate,
			draft.id,
		)
	}
	return true
}

func isRouteDraftOperation(operation tuiOperation) bool {
	return operation >= tuiOperationRouteID && operation <= tuiOperationRouteCreate
}

func queueSelectedRouteOperation(model *constellationTUIModel, operation tuiOperation, title string) (bool, bool) {
	if len(model.clusterRoutes) == 0 {
		model.dialog = infoDialog("NO ROUTE SELECTED", []string{"Create a route before running this action."})
		return true, false
	}
	index := minInt(maxInt(0, model.listIndex), len(model.clusterRoutes)-1)
	model.dialog = confirmationDialog(title, "APPLY", operation, model.clusterRoutes[index].ID)
	return true, false
}

func queueSelectedUserClusterAccess(model *constellationTUIModel) (bool, bool) {
	if len(model.users) == 0 {
		model.dialog = infoDialog("NO USER SELECTED", []string{"Create an active user first."})
		return true, false
	}
	index := minInt(maxInt(0, model.listIndex), len(model.users)-1)
	user := model.users[index]
	if user.Status != admin.StatusActive {
		model.dialog = infoDialog("USER IS REVOKED", []string{"Cluster access can be changed only for active credentials."})
		return true, false
	}
	model.dialog = confirmationDialog("TOGGLE FULL CLUSTER ACCESS", "APPLY", tuiOperationClusterUserAccess, user.ID)
	return true, false
}

func clusterEnrollmentDialog(operation tuiOperation, prompt, initial string) *tuiDialog {
	return &tuiDialog{kind: tuiDialogText, title: "ENROL CLUSTER NODE VIA SSH", prompt: prompt, input: initial, operation: operation, secret: operation == tuiOperationClusterEnrollPassword}
}

func advanceClusterEnrollmentDialog(model *constellationTUIModel, dialog *tuiDialog, value string) bool {
	if !isClusterEnrollmentOperation(dialog.operation) || dialog.operation == tuiOperationClusterEnrollConfirm {
		return false
	}
	draft := &model.clusterDraft
	switch dialog.operation {
	case tuiOperationClusterEnrollHost:
		draft.host = value
		model.dialog = clusterEnrollmentDialog(tuiOperationClusterEnrollPort, "SSH PORT", "22")
	case tuiOperationClusterEnrollPort:
		port, err := strconv.ParseUint(value, 10, 16)
		if err != nil || port == 0 {
			dialog.prompt = "VALID SSH PORT IS REQUIRED"
			return true
		}
		draft.port = uint16(port)
		model.dialog = clusterEnrollmentDialog(tuiOperationClusterEnrollUser, "SSH LOGIN", "root")
	case tuiOperationClusterEnrollUser:
		draft.user = value
		model.dialog = clusterEnrollmentDialog(tuiOperationClusterEnrollPassword, "SSH PASSWORD (MEMORY ONLY)", "")
	case tuiOperationClusterEnrollNodeID:
		draft.nodeID = value
		model.dialog = clusterEnrollmentDialog(tuiOperationClusterEnrollName, "DISPLAY NAME", "")
	case tuiOperationClusterEnrollName:
		draft.name = value
		model.dialog = clusterEnrollmentDialog(tuiOperationClusterEnrollRegion, "REGION", "")
	case tuiOperationClusterEnrollRegion:
		draft.region = value
		model.dialog = clusterEnrollmentDialog(tuiOperationClusterEnrollDomain, "PUBLIC VPN DOMAIN", "")
	case tuiOperationClusterEnrollDomain:
		if err := validateInstallDomain(value); err != nil {
			dialog.prompt = strings.ToUpper(err.Error())
			return true
		}
		draft.domain = value
		model.dialog = clusterEnrollmentDialog(tuiOperationClusterEnrollAddresses, "PUBLIC IP LIST (COMMA SEPARATED)", "")
	case tuiOperationClusterEnrollAddresses:
		parts := strings.Split(value, ",")
		addresses := make([]string, 0, len(parts))
		for _, part := range parts {
			address, err := netip.ParseAddr(strings.TrimSpace(part))
			if err != nil || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() {
				dialog.prompt = "VALID PUBLIC IP LIST IS REQUIRED"
				return true
			}
			addresses = append(addresses, address.String())
		}
		if len(addresses) == 0 || len(addresses) > 8 {
			dialog.prompt = "PROVIDE 1-8 PUBLIC IP ADDRESSES"
			return true
		}
		draft.addresses = addresses
		model.pending.operation = tuiOperationClusterDiscoverHostKey
		model.dialog = nil
	}
	return true
}

func isClusterEnrollmentOperation(operation tuiOperation) bool {
	return operation >= tuiOperationClusterEnrollHost && operation <= tuiOperationClusterEnrollConfirm
}

func (model *constellationTUIModel) clearClusterDraft() {
	zeroClusterEnrollmentDraft(&model.clusterDraft)
}

func (model *constellationTUIModel) takeClusterDraft() clusterEnrollmentDraft {
	draft := model.clusterDraft
	model.clusterDraft = clusterEnrollmentDraft{}
	return draft
}

func queueSelectedClusterNodeOperation(model *constellationTUIModel, operation tuiOperation, title string) (bool, bool) {
	if len(model.clusterNodes) == 0 {
		model.dialog = infoDialog("NO CLUSTER NODE SELECTED", []string{"Enrol a node before running this action."})
		return true, false
	}
	index := minInt(maxInt(0, model.listIndex), len(model.clusterNodes)-1)
	node := model.clusterNodes[index]
	if containsClusterRole(node.Roles, cluster.RoleMaster) {
		model.dialog = infoDialog("MASTER IS PROTECTED", []string{"The authoritative master cannot be changed by this action."})
		return true, false
	}
	model.dialog = confirmationDialog(title, "APPLY", operation, node.ID)
	return true, false
}

func containsClusterRole(roles []cluster.NodeRole, wanted cluster.NodeRole) bool {
	for _, role := range roles {
		if role == wanted {
			return true
		}
	}
	return false
}

func queueSelectedUserOperation(model *constellationTUIModel, operation tuiOperation, confirm bool) (bool, bool) {
	if len(model.users) == 0 {
		model.dialog = infoDialog("NO USER SELECTED", []string{"Create an active user before running this action."})
		return true, false
	}
	index := minInt(maxInt(0, model.listIndex), len(model.users)-1)
	user := model.users[index]
	if operation == tuiOperationUserDelete {
		if user.Status != admin.StatusRevoked {
			model.dialog = infoDialog("REVOKE FIRST", []string{"Use R to revoke the active credential before permanent deletion."})
			return true, false
		}
	} else if user.Status != admin.StatusActive {
		model.dialog = infoDialog("ACTION NOT AVAILABLE", []string{"The selected credential is revoked. It can only be permanently deleted."})
		return true, false
	}
	if confirm {
		verb := "ROTATE"
		if operation == tuiOperationUserRevoke {
			verb = "REVOKE"
		} else if operation == tuiOperationUserDelete {
			verb = "DELETE"
		}
		model.dialog = confirmationDialog(verb+" CREDENTIAL", verb, operation, user.ID)
		return true, false
	}
	model.pending = tuiPendingOperation{operation: operation, value: user.ID}
	return true, true
}

func confirmationDialog(title, token string, operation tuiOperation, value string) *tuiDialog {
	return &tuiDialog{kind: tuiDialogConfirm, title: title, prompt: "PRESS ENTER TO CONFIRM " + token, operation: operation, value: value}
}

func infoDialog(title string, lines []string) *tuiDialog {
	return &tuiDialog{kind: tuiDialogInfo, title: title, lines: lines}
}

func executeConstellationOperation(manager *admin.Manager, controller serviceController, model *constellationTUIModel) {
	pending := model.pending
	model.pending = tuiPendingOperation{}
	var output, failures bytes.Buffer
	code := 0
	title := "ACTION COMPLETE"

	switch pending.operation {
	case tuiOperationUserAdd:
		user, err := manager.AddUser(pending.value, pending.aux)
		if err != nil {
			fmt.Fprintf(&failures, "create user failed: %v\n", err)
			code = 1
			break
		}
		fmt.Fprintf(&output, "Created %s (%s) with automatic traffic adaptation.\n", user.Name, user.ID)
		if err := controller.Action("restart", &output, &failures); err != nil {
			fmt.Fprintf(&failures, "credential created, but restart failed: %v\n", err)
			code = 1
			break
		}
		appendCredentialBundle(manager, user.ID, &output, &failures)
		title = "USER CREATED // CLIENT SETTINGS"
	case tuiOperationUserExport:
		appendCredentialBundle(manager, pending.value, &output, &failures)
		if failures.Len() > 0 {
			code = 1
		}
		title = "CLIENT CREDENTIAL // KEEP SECRET"
	case tuiOperationUserRotate, tuiOperationUserRevoke:
		verb := "rotated"
		var err error
		if pending.operation == tuiOperationUserRotate {
			err = manager.RotateUser(pending.value)
		} else {
			verb = "revoked"
			err = manager.RevokeUser(pending.value)
		}
		if err != nil {
			fmt.Fprintf(&failures, "credential %s failed: %v\n", verb, err)
			code = 1
			break
		}
		if err := syncClusterUserCredential(manager, pending.value); err != nil {
			fmt.Fprintf(&failures, "credential changed locally, but cluster sync failed: %v\n", err)
			code = 1
		}
		fmt.Fprintf(&output, "Credential %s: %s\n", verb, pending.value)
		if err := controller.Action("restart", &output, &failures); err != nil {
			fmt.Fprintf(&failures, "server restart failed: %v\n", err)
			code = 1
		}
	case tuiOperationUserDelete:
		if err := manager.DeleteUser(pending.value); err != nil {
			fmt.Fprintf(&failures, "delete user failed: %v\n", err)
			code = 1
			break
		}
		if err := syncClusterUserCredential(manager, pending.value); err != nil {
			fmt.Fprintf(&failures, "user deleted locally, but cluster sync failed: %v\n", err)
			code = 1
		}
		fmt.Fprintf(&output, "Permanently deleted revoked user: %s\n", pending.value)
		if err := controller.Action("restart", &output, &failures); err != nil {
			fmt.Fprintf(&failures, "user deleted but server restart failed: %v\n", err)
			code = 1
		}
	case tuiOperationServiceStart, tuiOperationServiceStop, tuiOperationServiceRestart:
		action := map[tuiOperation]string{tuiOperationServiceStart: "start", tuiOperationServiceStop: "stop", tuiOperationServiceRestart: "restart"}[pending.operation]
		if err := controller.Action(action, &output, &failures); err != nil {
			fmt.Fprintf(&failures, "%s services failed: %v\n", action, err)
			code = 1
		} else {
			fmt.Fprintf(&output, "Service action completed: %s\n", action)
		}
	case tuiOperationServiceValidate:
		if err := controller.Validate(&output, &failures); err != nil {
			fmt.Fprintf(&failures, "configuration validation failed: %v\n", err)
			code = 1
		} else {
			fmt.Fprintln(&output, "Configuration validation passed.")
		}
	case tuiOperationServiceLogs:
		if err := controller.Logs(false, &output, &failures); err != nil {
			fmt.Fprintf(&failures, "read service events failed: %v\n", err)
			code = 1
		}
		model.output = splitTUIOutput(output.String()+failures.String(), 400)
		model.view = tuiViewEvents
	case tuiOperationDomainSet:
		addresses, err := resolveDomainAddresses(pending.value)
		if err != nil {
			fmt.Fprintf(&failures, "resolve domain failed: %v\n", err)
			code = 1
		} else {
			code = performDomainSet(manager, controller, pending.value, addresses, &output, &failures)
		}
	case tuiOperationFeatureProduction:
		code = performFeatureSet(manager, controller, true, &output, &failures)
	case tuiOperationFeatureCompatibility:
		code = performFeatureSet(manager, controller, false, &output, &failures)
	case tuiOperationBackupCreate:
		code = backupCommand(manager, controller, []string{"create"}, &output, &failures)
	case tuiOperationBackupRestore:
		code = backupCommand(manager, controller, []string{"restore", "--path", pending.value, "--confirm", "RESTORE"}, &output, &failures)
	case tuiOperationClusterToggleEnabled, tuiOperationClusterTogglePublished, tuiOperationClusterRemove:
		if err := mutateSelectedClusterNode(manager, pending.operation, pending.value); err != nil {
			fmt.Fprintf(&failures, "cluster node update failed: %v\n", err)
			code = 1
		} else {
			fmt.Fprintf(&output, "%s: %s\n", clusterNodeActionLabel(pending.operation), pending.value)
			if pending.operation == tuiOperationClusterRemove {
				if err := controller.Action("restart", &output, &failures); err != nil {
					fmt.Fprintf(&failures, "node removed but service restart failed: %v\n", err)
					code = 1
				}
			}
		}
	case tuiOperationClusterAssignUser:
		enabled, err := toggleClusterNodeForUserSynced(manager, pending.aux, pending.value, syncClusterUserCredentialToNodes)
		if err != nil {
			fmt.Fprintf(&failures, "cluster node assignment failed: %v\n", err)
			code = 1
		} else {
			fmt.Fprintf(&output, "node %s assigned=%t for user %s\n", pending.aux, enabled, pending.value)
		}
	case tuiOperationClusterUserAccess:
		enabled, err := toggleUserClusterAccessSynced(manager, pending.value, syncClusterUserCredentialToNodes)
		if err != nil {
			fmt.Fprintf(&failures, "cluster user access update failed: %v\n", err)
			code = 1
		} else {
			fmt.Fprintf(&output, "cluster access enabled=%t for user %s\n", enabled, pending.value)
		}
	case tuiOperationClusterSyncUsers:
		users, err := manager.ListUsers()
		if err != nil {
			fmt.Fprintf(&failures, "list users for cluster sync failed: %v\n", err)
			code = 1
			break
		}
		for _, user := range users {
			if err := syncClusterUserCredential(manager, user.ID); err != nil {
				fmt.Fprintf(&failures, "%s: %v\n", user.ID, err)
				code = 1
			}
		}
		if code == 0 {
			fmt.Fprintf(&output, "synchronized %d user credentials across allowed nodes\n", len(users))
		}
	case tuiOperationRouteCreate:
		if err := createClusterRouteForUsersSynced(manager, model.routeDraft, syncClusterUserCredentialToNodes); err != nil {
			fmt.Fprintf(&failures, "create route failed: %v\n", err)
			code = 1
		} else {
			fmt.Fprintf(&output, "route created: %s\n", model.routeDraft.id)
			fmt.Fprintf(&output, "assigned users: %d\n", len(model.routeDraft.userIDs))
		}
		model.routeDraft = clusterRouteDraft{}
	case tuiOperationRouteToggleEnabled, tuiOperationRouteRemove:
		if err := mutateClusterRoute(manager, pending.operation, pending.value); err != nil {
			fmt.Fprintf(&failures, "route update failed: %v\n", err)
			code = 1
		} else {
			fmt.Fprintf(&output, "route updated: %s\n", pending.value)
		}
	case tuiOperationRouteAssignUser:
		if err := assignRouteToUserSynced(manager, pending.aux, pending.value, syncClusterUserCredentialToNodes); err != nil {
			fmt.Fprintf(&failures, "route assignment failed: %v\n", err)
			code = 1
		} else {
			fmt.Fprintf(&output, "route %s assigned to user %s\n", pending.aux, pending.value)
		}
	case tuiOperationGeoDataSchedule:
		if err := setGeoDataSchedule(manager.RootDirectory(), pending.value); err != nil {
			fmt.Fprintf(&failures, "geodata schedule update failed: %v\n", err)
			code = 1
		} else {
			fmt.Fprintf(&output, "automatic GeoData update schedule: %s\n", pending.value)
		}
	default:
		return
	}

	combined := splitTUIOutput(output.String()+failures.String(), 400)
	if pending.operation != tuiOperationServiceLogs {
		model.output = combined
		if len(combined) == 0 {
			combined = []string{"Operation completed without additional output."}
		}
		if code != 0 {
			title = "ACTION FAILED // REVIEW OUTPUT"
		}
		model.dialog = infoDialog(title, combined)
	}
	if err := model.refresh(manager, controller, time.Now(), true); err != nil {
		model.status = "REFRESH FAILED // " + boundedDisplay(err.Error(), 40)
	} else if code != 0 {
		model.status = "ACTION FAILED"
	} else {
		model.status = "ACTION COMPLETE"
	}
}

func appendCredentialBundle(manager *admin.Manager, identifier string, output, failures io.Writer) {
	profile, err := manager.ExportUserProfile(identifier)
	if err != nil {
		fmt.Fprintf(failures, "export credential failed: %v\n", err)
		return
	}
	uri, err := onboarding.EncodeURI(profile)
	if err != nil {
		fmt.Fprintf(failures, "encode credential failed: %v\n", err)
		return
	}
	fmt.Fprintln(output, "WARNING: bearer credential; show only to the intended device.")
	fmt.Fprintln(output, "")
	fmt.Fprintln(output, "MANUAL SETTINGS")
	writeManualProfile(output, profile)
	fmt.Fprintf(output, "\nImport URI:\n%s\n\nCLIENT QR\n", uri)
	if err := runQR([]string{"-t", "UTF8", "-o", "-"}, uri, output, failures); err != nil {
		fmt.Fprintf(failures, "terminal QR unavailable: %v\n", err)
	}
}
