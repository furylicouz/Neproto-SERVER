package main

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"neproto.local/chameleon/internal/buildinfo"
	"neproto.local/chameleon/internal/cluster"
)

var (
	tuiBackground = tcell.NewRGBColor(2, 10, 14)
	tuiPanel      = tcell.NewRGBColor(4, 20, 26)
	tuiCyan       = tcell.NewRGBColor(18, 229, 255)
	tuiDimCyan    = tcell.NewRGBColor(30, 116, 127)
	tuiGreen      = tcell.NewRGBColor(71, 255, 174)
	tuiAmber      = tcell.NewRGBColor(255, 190, 52)
	tuiMagenta    = tcell.NewRGBColor(228, 73, 255)
	tuiWhite      = tcell.NewRGBColor(206, 238, 242)
	tuiMuted      = tcell.NewRGBColor(91, 126, 132)
)

func renderConstellationTUI(screen tcell.Screen, model *constellationTUIModel) {
	screen.SetStyle(tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground))
	screen.Clear()
	width, height := screen.Size()
	if width < 100 || height < 30 {
		renderCompactConstellationTUI(screen, model, width, height)
		screen.Show()
		return
	}

	renderTUIHeader(screen, model, width)
	bodyTop, bodyBottom := 4, height-8
	leftWidth := maxInt(27, width*23/100)
	rightWidth := maxInt(29, width*24/100)
	centerLeft := leftWidth + 1
	rightLeft := width - rightWidth

	drawTUIBox(screen, 0, bodyTop, leftWidth, bodyBottom, " SYSTEM MONITOR ", tuiCyan)
	drawTUIBox(screen, centerLeft, bodyTop, rightLeft-1, bodyBottom, " "+tuiWorkspaceTitle(model.view)+" ", tuiCyan)
	drawTUIBox(screen, rightLeft, bodyTop, width-1, bodyBottom, " NETWORK MAP ", tuiCyan)
	renderSystemPanel(screen, model, 0, bodyTop, leftWidth, bodyBottom)
	renderControlPanel(screen, model, centerLeft, bodyTop, rightLeft-1, bodyBottom)
	renderNetworkPanel(screen, model, rightLeft, bodyTop, width-1, bodyBottom)
	renderFilesystemPanel(screen, model, 0, height-7, width-1, height-2)

	footerStyle := tcell.StyleDefault.Foreground(tuiDimCyan).Background(tuiBackground)
	putTUIText(screen, 1, height-1, width-2, tuiFooter(model), footerStyle)
	renderTUIDialog(screen, model)
	screen.Show()
}

func renderCompactConstellationTUI(screen tcell.Screen, model *constellationTUIModel, width, height int) {
	titleStyle := tcell.StyleDefault.Foreground(tuiCyan).Background(tuiBackground).Bold(true)
	mutedStyle := tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground)
	putTUICentered(screen, 0, width, "NP/2 CONSTELLATION", titleStyle)
	putTUICentered(screen, 2, width, "Resize terminal to at least 100x30 for full eDEX layout", mutedStyle)
	if width > 4 && height > 7 {
		drawTUIBox(screen, 1, 4, width-2, height-3, " CONTROL ", tuiDimCyan)
		available := height - 8
		for index, action := range constellationTUIActions {
			if index >= available {
				break
			}
			prefix := "  "
			style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
			if index == model.selected {
				prefix = "> "
				style = style.Foreground(tuiBackground).Background(tuiCyan).Bold(true)
			}
			putTUIText(screen, 3, 6+index, width-6, prefix+action.label, style)
		}
	}
	putTUIText(screen, 2, height-1, width-4, "↑↓ Navigate  Enter Open  q Quit", mutedStyle)
	renderTUIDialog(screen, model)
}

func renderTUIHeader(screen tcell.Screen, model *constellationTUIModel, width int) {
	cyanBold := tcell.StyleDefault.Foreground(tuiCyan).Background(tuiBackground).Bold(true)
	muted := tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground)
	clock := model.now.Format("15:04:05")
	if model.now.IsZero() {
		clock = "--:--:--"
	}
	putTUIText(screen, 1, 0, 12, clock, cyanBold)
	putTUICentered(screen, 0, width, "NEPROTO // NP/2 CONSTELLATION", cyanBold)
	putTUIRight(screen, width-1, 0, "CORE "+buildinfo.Version, muted)

	tabs := "[F1 STATUS] [F2 USERS] [F3 CLUSTER] [F4 ROUTES] [F5 SERVICE] [F6 DOMAIN] [F7 BACKUP] [F8 FILES] [F10 QUIT]"
	putTUICentered(screen, 2, width, tabs, muted)
	drawTUIHorizontal(screen, 0, width-1, 3, tuiDimCyan)
}

func renderSystemPanel(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	x, y, innerWidth := left+2, top+2, right-left-3
	label := tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground)
	value := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
	green := tcell.StyleDefault.Foreground(tuiGreen).Background(tuiBackground).Bold(true)
	putTUIText(screen, x, y, innerWidth, "HOST", label)
	putTUIText(screen, x+8, y, innerWidth-8, model.host.Hostname, green)
	y += 2
	putTUIText(screen, x, y, innerWidth, "UPTIME", label)
	putTUIText(screen, x+8, y, innerWidth-8, model.host.Uptime, value)
	y++
	putTUIText(screen, x, y, innerWidth, "LOAD", label)
	putTUIText(screen, x+8, y, innerWidth-8, model.host.Load, value)
	y += 2
	putTUIText(screen, x, y, innerWidth, "MEMORY  "+model.host.Memory, label)
	y++
	renderTUIProgress(screen, x, y, innerWidth, model.host.MemoryPercent)
	y += 2
	putTUIText(screen, x, y, innerWidth, "NETWORK TRAFFIC", label)
	y++
	putTUIText(screen, x, y, innerWidth, "RX "+formatTUIRate(model.rxRate), green)
	y++
	putTUIText(screen, x, y, innerWidth, tuiSparkline(model.rxHistory, innerWidth), tcell.StyleDefault.Foreground(tuiCyan).Background(tuiBackground))
	y += 2
	putTUIText(screen, x, y, innerWidth, "TX "+formatTUIRate(model.txRate), tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground))
	y++
	putTUIText(screen, x, y, innerWidth, tuiSparkline(model.txHistory, innerWidth), tcell.StyleDefault.Foreground(tuiMagenta).Background(tuiBackground))
	y += 2
	if y < bottom {
		putTUIText(screen, x, y, innerWidth, "TOTAL RX  "+model.host.NetworkRX, label)
		y++
		putTUIText(screen, x, y, innerWidth, "TOTAL TX  "+model.host.NetworkTX, label)
	}
}

func renderControlPanel(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	if model.view != tuiViewDashboard {
		renderTUIWorkspace(screen, model, left, top, right, bottom)
		return
	}
	x, y, innerWidth := left+2, top+2, right-left-3
	muted := tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground)
	putTUIText(screen, x, y, innerWidth, "CARRIER FABRIC", muted)
	y++
	carriers := []struct{ name, state string }{
		{"HTTPS/WSS", model.snapshot.services.Ingress},
		{"HTTP/3", enabledLabel(model.snapshot.installation.EnableConstellation)},
		{"WEBRTC", enabledLabel(model.snapshot.installation.EnableConstellation)},
	}
	for _, carrier := range carriers {
		stateColor := tuiGreen
		if carrier.state != "active" && carrier.state != "enabled" {
			stateColor = tuiAmber
		}
		putTUIText(screen, x, y, innerWidth/2, carrier.name, tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground))
		putTUIRight(screen, right-2, y, strings.ToUpper(carrier.state), tcell.StyleDefault.Foreground(stateColor).Background(tuiBackground).Bold(true))
		y++
	}
	y++
	putTUIText(screen, x, y, innerWidth, "COMMAND MATRIX", muted)
	y++
	maxRows := bottom - y - 1
	for index, action := range constellationTUIActions {
		if index >= maxRows {
			break
		}
		style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
		functionKey := index + 1
		if action.short == "QUIT" {
			functionKey = 10
		}
		prefix := fmt.Sprintf(" F%d  ", functionKey)
		if index == model.selected {
			style = style.Foreground(tuiBackground).Background(tuiCyan).Bold(true)
			prefix = fmt.Sprintf(">F%d  ", functionKey)
		}
		putTUIText(screen, x, y+index, innerWidth, padTUIText(prefix+action.label, innerWidth), style)
	}
	if model.status != "" {
		putTUIText(screen, x, bottom-1, innerWidth, model.status, tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground))
	}
}

func renderNetworkPanel(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	x, y, innerWidth := left+2, top+2, right-left-3
	muted := tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground)
	cyan := tcell.StyleDefault.Foreground(tuiCyan).Background(tuiBackground)
	green := tcell.StyleDefault.Foreground(tuiGreen).Background(tuiBackground).Bold(true)
	putTUICenteredIn(screen, x, y, innerWidth, "MAPSCII // WORLD LINK", cyan.Bold(true))
	mapHeight := minInt(10, maxInt(4, bottom-top-18))
	y++
	renderTUIBrailleNetworkMap(screen, x, y, innerWidth, mapHeight, model.mapState, model.clusterNodes, model.clusterHealth, model.now, false)
	y += mapHeight + 1
	installation := model.snapshot.installation
	putTUIText(screen, x, y, innerWidth, "DOMAIN", muted)
	y++
	putTUIText(screen, x, y, innerWidth, installation.Domain, green)
	y += 2
	putTUIText(screen, x, y, innerWidth, "PUBLIC NODE", muted)
	y++
	putTUIText(screen, x, y, innerWidth, strings.Join(installation.ServerAddresses, ", "), cyan)
	y += 2
	putTUIText(screen, x, y, innerWidth, "NP/2 CORE", muted)
	putTUIRight(screen, right-2, y, strings.ToUpper(displayState(model.snapshot.services.NP2)), serviceStyle(model.snapshot.services.NP2))
	y++
	putTUIText(screen, x, y, innerWidth, "WEB ADMIN", muted)
	putTUIRight(screen, right-2, y, strings.ToUpper(displayState(model.snapshot.services.Web)), serviceStyle(model.snapshot.services.Web))
	y++
	putTUIText(screen, x, y, innerWidth, "EDGE", muted)
	putTUIRight(screen, right-2, y, strings.ToUpper(displayState(model.snapshot.services.Ingress)), serviceStyle(model.snapshot.services.Ingress))
	y += 2
	if y < bottom {
		renderDashboardClusterSummary(screen, model, x, &y, innerWidth, bottom)
	}
	if y < bottom {
		putTUIText(screen, x, y, innerWidth, fmt.Sprintf("USERS  %d ACTIVE / %d REVOKED", model.snapshot.activeUsers, model.snapshot.revokedUsers), muted)
		y++
	}
	if y < bottom {
		putTUIText(screen, x, y, innerWidth, fmt.Sprintf("BACKUPS  %d VERIFIED", model.snapshot.backups), muted)
	}
}

func renderDashboardClusterSummary(
	screen tcell.Screen,
	model *constellationTUIModel,
	x int,
	y *int,
	width int,
	bottom int,
) {
	if len(model.clusterNodes) == 0 {
		putTUIText(screen, x, *y, width, "CLUSTER  NOT INITIALIZED", tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground).Bold(true))
		(*y)++
		return
	}
	up, enabled := 0, 0
	for _, node := range model.clusterNodes {
		if !node.Enabled {
			continue
		}
		enabled++
		if health, exists := model.clusterHealth[node.ID]; exists && health.status == "UP" {
			up++
		}
	}
	summaryColor := tuiAmber
	if enabled > 0 && up == enabled {
		summaryColor = tuiGreen
	}
	putTUIText(
		screen, x, *y, width,
		fmt.Sprintf("CLUSTER  %d/%d UP", up, enabled),
		tcell.StyleDefault.Foreground(summaryColor).Background(tuiBackground).Bold(true),
	)
	(*y)++

	// Preserve two rows for the user and backup summaries on standard layouts.
	visibleNodes := minInt(len(model.clusterNodes), maxInt(0, bottom-*y-2))
	for index := 0; index < visibleNodes; index++ {
		node := model.clusterNodes[index]
		state, latency := dashboardClusterNodeState(node, model.clusterHealth[node.ID])
		stateText := state
		if state == "UP" && latency > 0 {
			stateText += " " + latency.Round(time.Millisecond).String()
		}
		style := tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground)
		symbol := "×"
		if state == "UP" {
			style = style.Foreground(tuiGreen)
			symbol = "●"
		} else if state == "DRAIN" {
			style = style.Foreground(tuiMuted)
			symbol = "◇"
		}
		nameWidth := maxInt(1, width-len([]rune(stateText))-4)
		line := fmt.Sprintf("%s %-*s %s", symbol, nameWidth, truncateRunes(node.Name, nameWidth), stateText)
		putTUIText(screen, x, *y, width, line, style)
		(*y)++
	}
}

func dashboardClusterNodeState(node cluster.Node, health clusterNodeHealth) (string, time.Duration) {
	if !node.Enabled {
		return "DRAIN", 0
	}
	if health.status == "UP" {
		return "UP", health.latency
	}
	if health.status == "DOWN" {
		return "DOWN", 0
	}
	return "CHECK", 0
}

func tuiWorkspaceTitle(view tuiView) string {
	switch view {
	case tuiViewStatus:
		return "DIAGNOSTIC CORE"
	case tuiViewUsers:
		return "USER ACCESS MATRIX"
	case tuiViewCluster:
		return "CLUSTER FABRIC"
	case tuiViewRoutes:
		return "ROUTE POLICY"
	case tuiViewService:
		return "SERVICE CONTROL"
	case tuiViewDomain:
		return "IDENTITY CONFIG"
	case tuiViewBackups:
		return "RECOVERY VAULT"
	case tuiViewFiles:
		return "SECURE FILESYSTEM"
	case tuiViewEvents:
		return "EVENT STREAM"
	case tuiViewMap:
		return "GLOBAL NETWORK MAP"
	default:
		return "CONTROL TERMINAL"
	}
}

func tuiFooter(model *constellationTUIModel) string {
	if model.view == tuiViewMap {
		return "ARROWS PAN   A/Z ZOOM   C RESET   ESC/Q BACK   F1-F8 JUMP   F10 QUIT"
	}
	if model.view != tuiViewDashboard {
		return "↑↓/J/K SELECT   R REFRESH   PGUP/PGDN SCROLL   ESC/Q BACK   F1-F8 JUMP   F10 QUIT"
	}
	return "↑↓/J/K NAVIGATE   ENTER OPEN   F1-F8 SHORTCUTS   R REFRESH   Q QUIT"
}

func renderFilesystemPanel(screen tcell.Screen, model *constellationTUIModel, left, top, right, bottom int) {
	drawTUIBox(screen, left, top, right, bottom, " SECURE FILESYSTEM ", tuiDimCyan)
	items := []struct{ name, detail string }{
		{"CONFIG", "VALIDATED"}, {"USERS", fmt.Sprintf("%d ACTIVE", model.snapshot.activeUsers)},
		{"CERTS", "LOCKED"}, {"BACKUPS", fmt.Sprintf("%d READY", model.snapshot.backups)}, {"EVENTS", "READ ONLY"},
	}
	innerWidth := right - left - 3
	cardWidth := maxInt(15, innerWidth/len(items))
	for index, item := range items {
		x := left + 2 + index*cardWidth
		if x >= right-2 {
			break
		}
		putTUIText(screen, x, top+2, cardWidth-1, "▣ "+item.name, tcell.StyleDefault.Foreground(tuiCyan).Background(tuiBackground).Bold(true))
		putTUIText(screen, x, top+3, cardWidth-1, item.detail, tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground))
	}
}

func drawTUIBox(screen tcell.Screen, left, top, right, bottom int, title string, color tcell.Color) {
	if right <= left || bottom <= top {
		return
	}
	style := tcell.StyleDefault.Foreground(color).Background(tuiBackground)
	for x := left + 1; x < right; x++ {
		screen.SetContent(x, top, '─', nil, style)
		screen.SetContent(x, bottom, '─', nil, style)
	}
	for y := top + 1; y < bottom; y++ {
		screen.SetContent(left, y, '│', nil, style)
		screen.SetContent(right, y, '│', nil, style)
	}
	screen.SetContent(left, top, '┌', nil, style)
	screen.SetContent(right, top, '┐', nil, style)
	screen.SetContent(left, bottom, '└', nil, style)
	screen.SetContent(right, bottom, '┘', nil, style)
	putTUIText(screen, left+2, top, right-left-3, title, style.Bold(true))
}

func drawTUIHorizontal(screen tcell.Screen, left, right, y int, color tcell.Color) {
	style := tcell.StyleDefault.Foreground(color).Background(tuiBackground)
	for x := left; x <= right; x++ {
		screen.SetContent(x, y, '─', nil, style)
	}
}

func renderTUIProgress(screen tcell.Screen, x, y, width int, percent uint64) {
	if percent > 100 {
		percent = 100
	}
	filled := int(percent) * width / 100
	for index := range width {
		color, runeValue := tuiDimCyan, '░'
		if index < filled {
			color, runeValue = tuiCyan, '█'
		}
		screen.SetContent(x+index, y, runeValue, nil, tcell.StyleDefault.Foreground(color).Background(tuiBackground))
	}
}

func tuiSparkline(values []uint64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	if len(values) == 0 {
		return strings.Repeat("▁", width)
	}
	var maximum uint64
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	output := strings.Repeat("▁", width-len(values))
	for _, value := range values {
		level := 0
		if maximum > 0 {
			level = int(value * uint64(len(levels)-1) / maximum)
		}
		output += string(levels[level])
	}
	return output
}

func formatTUIRate(bytesPerSecond uint64) string { return formatByteCount(bytesPerSecond) + "/s" }

func serviceStyle(state string) tcell.Style {
	color := tuiAmber
	if state == "active" {
		color = tuiGreen
	}
	return tcell.StyleDefault.Foreground(color).Background(tuiBackground).Bold(true)
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func putTUIText(screen tcell.Screen, x, y, maximum int, value string, style tcell.Style) {
	if maximum <= 0 {
		return
	}
	position := 0
	for _, runeValue := range value {
		if position >= maximum {
			break
		}
		screen.SetContent(x+position, y, runeValue, nil, style)
		position++
	}
}

func putTUICentered(screen tcell.Screen, y, width int, value string, style tcell.Style) {
	putTUICenteredIn(screen, 0, y, width, value, style)
}

func putTUICenteredIn(screen tcell.Screen, x, y, width int, value string, style tcell.Style) {
	length := utf8.RuneCountInString(value)
	if length > width {
		length = width
	}
	putTUIText(screen, x+maxInt(0, (width-length)/2), y, width, value, style)
}

func putTUIRight(screen tcell.Screen, right, y int, value string, style tcell.Style) {
	length := utf8.RuneCountInString(value)
	putTUIText(screen, maxInt(0, right-length+1), y, length, value, style)
}

func padTUIText(value string, width int) string {
	length := utf8.RuneCountInString(value)
	if length >= width {
		return value
	}
	return value + strings.Repeat(" ", width-length)
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
