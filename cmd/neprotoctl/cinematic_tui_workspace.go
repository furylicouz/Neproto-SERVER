package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"neproto.local/chameleon/internal/cluster"
)

func renderTUIWorkspace(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	switch model.view {
	case tuiViewStatus:
		renderTUIOutputWorkspace(screen, model, left, top, right, bottom, "PRODUCTION DIAGNOSTICS", "R RUN DOCTOR")
	case tuiViewUsers:
		renderTUIUsersWorkspace(screen, model, left, top, right, bottom)
	case tuiViewCluster:
		renderTUIClusterWorkspace(screen, model, left, top, right, bottom)
	case tuiViewRoutes:
		renderTUIRoutesWorkspace(screen, model, left, top, right, bottom)
	case tuiViewService:
		renderTUIServiceWorkspace(screen, model, left, top, right, bottom)
	case tuiViewDomain:
		renderTUIDomainWorkspace(screen, model, left, top, right, bottom)
	case tuiViewBackups:
		renderTUIBackupsWorkspace(screen, model, left, top, right, bottom)
	case tuiViewFiles:
		renderTUIFilesWorkspace(screen, model, left, top, right, bottom)
	case tuiViewEvents:
		renderTUIOutputWorkspace(screen, model, left, top, right, bottom, "SANITIZED EVENT STREAM", "R RELOAD EVENTS")
	case tuiViewMap:
		renderTUIMapWorkspace(screen, model, left, top, right, bottom)
	}
}

func renderTUIClusterWorkspace(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	x, y, width := left+2, top+2, right-left-3
	muted := tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground)
	putTUIText(screen, x, y, width, fmt.Sprintf("CLUSTER FABRIC // REVISION %d // %d NODES", model.clusterRevision, len(model.clusterNodes)), muted)
	y += 2
	putTUIText(screen, x, y, width, padTUIText(" STATE NODE                 REGION        ROLE       CLIENT", width), tcell.StyleDefault.Foreground(tuiCyan).Background(tuiPanel).Bold(true))
	y++
	visible := maxInt(1, bottom-y-4)
	start := 0
	if model.listIndex >= visible {
		start = model.listIndex - visible + 1
	}
	for row, node := range model.clusterNodes[start:] {
		if row >= visible {
			break
		}
		index := start + row
		state := "DOWN"
		if health, exists := model.clusterHealth[node.ID]; exists {
			state = health.status
		} else if !node.Enabled {
			state = "DRAIN"
		}
		client := "HIDDEN"
		if node.ClientVisible {
			client = "PUBLISHED"
		}
		line := fmt.Sprintf(" %-5s %-20s %-13s %-10s %s", state, truncateRunes(node.Name, 20), truncateRunes(node.Region, 13), truncateRunes(clusterRoleLabel(node.Roles), 10), client)
		style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
		if index == model.listIndex {
			style = style.Foreground(tuiBackground).Background(tuiCyan).Bold(true)
		}
		putTUIText(screen, x, y+row, width, padTUIText(line, width), style)
	}
	if len(model.clusterNodes) == 0 {
		putTUIText(screen, x, y, width, "CLUSTER NOT INITIALIZED // PRESS N", tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground))
	}
	putTUIText(screen, x, bottom-2, width, "N ENROL   E ENABLE/DRAIN   P PUBLISH   A ASSIGN USER   S SYNC   D REMOVE", tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground).Bold(true))
}

func renderTUIRoutesWorkspace(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	x, y, width := left+2, top+2, right-left-3
	muted := tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground)
	putTUIText(screen, x, y, width, fmt.Sprintf("ROUTE POLICY // REVISION %d // %d RULES", model.clusterRevision, len(model.clusterRoutes)), muted)
	y++
	geodataLine := "GEODATA // " + strings.ToUpper(model.geodata.State) + " // AUTO " + strings.ToUpper(model.geodataSchedule)
	if !model.geodata.UpdatedAt.IsZero() {
		geodataLine += " // " + model.geodata.UpdatedAt.Local().Format("2006-01-02 15:04")
	}
	putTUIText(screen, x, y, width, geodataLine, tcell.StyleDefault.Foreground(tuiGreen).Background(tuiBackground))
	y += 2
	putTUIText(screen, x, y, width, padTUIText(" ST  PRI  ROUTE              MATCH              USERS VIA", width), tcell.StyleDefault.Foreground(tuiCyan).Background(tuiPanel).Bold(true))
	y++
	visible := maxInt(1, bottom-y-4)
	start := 0
	if model.listIndex >= visible {
		start = model.listIndex - visible + 1
	}
	for row, route := range model.clusterRoutes[start:] {
		if row >= visible {
			break
		}
		index := start + row
		state := "OFF"
		if route.Enabled {
			state = "ON"
		}
		via := strings.Join(route.Action.NodeIDs, ">")
		if via == "" {
			via = strings.ToUpper(string(route.Action.Kind))
		} else {
			via = strings.ToUpper(string(route.Action.Kind)) + ":" + via
		}
		line := fmt.Sprintf(" %-3s %-4d %-18s %-18s %-5d %s", state, route.Priority, truncateRunes(route.Name, 18), truncateRunes(clusterRouteMatchLabel(route.Match), 18), clusterRouteUserCount(model.clusterAccess, route.ID), via)
		style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
		if route.Mandatory {
			style = style.Foreground(tuiAmber)
		}
		if index == model.listIndex {
			style = style.Foreground(tuiBackground).Background(tuiCyan).Bold(true)
		}
		putTUIText(screen, x, y+row, width, padTUIText(line, width), style)
	}
	putTUIText(screen, x, bottom-2, width, "N NEW E TOGGLE A ASSIGN D DELETE G UPDATE T SCHEDULE R REFRESH ESC BACK", tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground).Bold(true))
}

func clusterRouteMatchLabel(match cluster.RouteMatch) string {
	if len(match.GeoSiteCategories) > 0 {
		return "GEOSITE:" + strings.Join(match.GeoSiteCategories, ",")
	}
	if len(match.GeoIPCountries) > 0 {
		return "GEOIP:" + strings.ToUpper(strings.Join(match.GeoIPCountries, ","))
	}
	if len(match.DomainSuffixes) > 0 {
		return "DOMAIN:" + strings.Join(match.DomainSuffixes, ",")
	}
	if len(match.CIDRs) > 0 {
		return "IP:" + strings.Join(match.CIDRs, ",")
	}
	return "ANY"
}

func clusterRouteUserCount(access []cluster.UserAccess, routeID string) int {
	count := 0
	for _, user := range access {
		if containsString(user.AllowedRouteIDs, routeID) {
			count++
		}
	}
	return count
}

func clusterRoleLabel(roles []cluster.NodeRole) string {
	if len(roles) == 0 {
		return "UNKNOWN"
	}
	values := make([]string, 0, len(roles))
	for _, role := range roles {
		values = append(values, strings.ToUpper(string(role)))
	}
	return strings.Join(values, "+")
}

func renderTUIUsersWorkspace(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	x, y, width := left+2, top+2, right-left-3
	muted := tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground)
	putTUIText(screen, x, y, width, fmt.Sprintf("USER ACCESS MATRIX // %d ACTIVE // %d REVOKED", model.snapshot.activeUsers, model.snapshot.revokedUsers), muted)
	y += 2
	putTUIText(screen, x, y, width, padTUIText(" ST  NAME                       MODE          CREDENTIAL", width), tcell.StyleDefault.Foreground(tuiCyan).Background(tuiPanel).Bold(true))
	y++
	visible := maxInt(1, bottom-y-4)
	start := 0
	if model.listIndex >= visible {
		start = model.listIndex - visible + 1
	}
	for row, user := range model.users[start:] {
		if row >= visible {
			break
		}
		index := start + row
		status := "●"
		if user.Status != "active" {
			status = "×"
		}
		line := fmt.Sprintf(" %s   %-26s %-13s %s", status, truncateRunes(user.Name, 26), "automatic", user.ID)
		style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
		if index == model.listIndex {
			style = style.Foreground(tuiBackground).Background(tuiCyan).Bold(true)
		}
		putTUIText(screen, x, y+row, width, padTUIText(line, width), style)
	}
	putTUIText(screen, x, bottom-2, width, "N NEW E EXPORT C CLUSTER O ROTATE R REVOKE D DELETE ESC BACK", tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground).Bold(true))
}

func renderTUIServiceWorkspace(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	x, y, width := left+2, top+2, right-left-3
	putTUIText(screen, x, y, width, "RUNTIME CONTROL BUS", tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground))
	y += 2
	rows := []string{
		"NP/2 CORE                         " + strings.ToUpper(displayState(model.snapshot.services.NP2)),
		"WEB ADMIN                         " + strings.ToUpper(displayState(model.snapshot.services.Web)),
		"CADDY EDGE                        " + strings.ToUpper(displayState(model.snapshot.services.Ingress)),
		"", "[1] START ALL SERVICES", "[2] STOP ALL SERVICES", "[3] RESTART ALL SERVICES",
		"[4] VALIDATE CONFIGURATION", "[5] LOAD LAST 200 EVENTS",
	}
	for index, row := range rows {
		style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
		if strings.HasSuffix(row, "ACTIVE") {
			style = style.Foreground(tuiGreen)
		}
		putTUIText(screen, x, y+index, width, row, style)
	}
	putTUIText(screen, x, bottom-2, width, "1-5 EXECUTE   DESTRUCTIVE ACTIONS REQUIRE CONFIRMATION   ESC BACK", tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground))
}

func renderTUIDomainWorkspace(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	x, y, width := left+2, top+2, right-left-3
	installation := model.snapshot.installation
	rows := []string{
		"PUBLIC IDENTITY", installation.Domain, "", "PUBLIC ADDRESSES", strings.Join(installation.ServerAddresses, ", "), "",
		"CONSTELLATION     " + strings.ToUpper(enabledLabel(installation.EnableConstellation)),
		"FORWARD SECRECY   " + strings.ToUpper(enabledLabel(installation.EnableForwardSecrecy)), "",
		"[D] CHANGE DOMAIN", "[P] PRODUCTION POLICY", "[C] COMPATIBILITY POLICY", "[V] VALIDATE CONFIGURATION",
	}
	for index, row := range rows {
		style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
		if index == 0 || index == 3 {
			style = style.Foreground(tuiMuted)
		}
		putTUIText(screen, x, y+index, width, row, style)
	}
	putTUIText(screen, x, bottom-2, width, "CHANGES CREATE ROLLBACK SNAPSHOT + READINESS PROBE   ESC BACK", tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground))
}

func renderTUIBackupsWorkspace(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	x, y, width := left+2, top+2, right-left-3
	putTUIText(screen, x, y, width, fmt.Sprintf("RECOVERY VAULT // %d VERIFIED SNAPSHOTS", len(model.backups)), tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground))
	y += 2
	visible := maxInt(1, bottom-y-4)
	for index, path := range model.backups {
		if index >= visible {
			break
		}
		style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
		if index == model.listIndex {
			style = style.Foreground(tuiBackground).Background(tuiCyan).Bold(true)
		}
		putTUIText(screen, x, y+index, width, padTUIText(fmt.Sprintf(" %02d  %s", index+1, filepath.Base(path)), width), style)
	}
	putTUIText(screen, x, bottom-2, width, "N CREATE SNAPSHOT   ENTER RESTORE SELECTED   R REFRESH   ESC BACK", tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground))
}

func renderTUIFilesWorkspace(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	x, y, width := left+2, top+2, right-left-3
	rows := []string{
		"▣ CONFIGURATION", "  /etc/neproto/server.json                  VALIDATED", "",
		"▣ USER CREDENTIAL VAULT", fmt.Sprintf("  active=%d  revoked=%d                     MANAGED", model.snapshot.activeUsers, model.snapshot.revokedUsers), "",
		"▣ CERTIFICATE VAULT", "  /etc/neproto/tls                          CONTENT HIDDEN", "",
		"▣ RECOVERY SNAPSHOTS", fmt.Sprintf("  /var/backups/neproto                      %d READY", len(model.backups)), "",
		"▣ SERVICE EVENTS", "  journald                                  SANITIZED / READ ONLY",
	}
	for index, row := range rows {
		style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
		if strings.HasPrefix(row, "▣") {
			style = style.Foreground(tuiCyan).Bold(true)
		}
		putTUIText(screen, x, y+index, width, row, style)
	}
}

func renderTUIOutputWorkspace(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int, title, hint string) {
	x, y, width := left+2, top+2, right-left-3
	putTUIText(screen, x, y, width, title, tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground))
	y += 2
	visible := maxInt(1, bottom-y-3)
	if len(model.output) == 0 {
		putTUIText(screen, x, y, width, "NO DATA // PRESS R TO EXECUTE", tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground))
	} else {
		start := minInt(model.scroll, maxInt(0, len(model.output)-1))
		for index, line := range model.output[start:] {
			if index >= visible {
				break
			}
			style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
			if strings.Contains(line, "[OK]") || strings.Contains(line, "passed") {
				style = style.Foreground(tuiGreen)
			} else if strings.Contains(line, "[FAIL]") || strings.Contains(strings.ToLower(line), "error") {
				style = style.Foreground(tuiAmber)
			}
			putTUIText(screen, x, y+index, width, line, style)
		}
	}
	putTUIText(screen, x, bottom-2, width, hint+"   PGUP/PGDN SCROLL   ESC BACK", tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground))
}

func renderTUIMapWorkspace(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	x, y, width := left+2, top+2, right-left-3
	height := maxInt(1, bottom-top-6)
	renderTUIBrailleNetworkMap(screen, x, y, width, height, model.mapState, model.clusterNodes, model.clusterHealth, model.now, true)
	located := len(locateTUIClusterNodes(model.clusterNodes))
	putTUIText(screen, x, bottom-3, width, fmt.Sprintf("◆ MASTER  ● EDGE  · CLUSTER LINK  • LIVE PULSE  // GEO %d/%d", located, len(model.clusterNodes)), tcell.StyleDefault.Foreground(tuiGreen).Background(tuiBackground))
	putTUIText(screen, x, bottom-2, width, fmt.Sprintf("CENTER %.1f,%.1f  ZOOM %.2fx  // ARROWS PAN  A/Z ZOOM  C RESET", model.mapState.centerLat, model.mapState.centerLon, model.mapState.zoom), tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground))
}
