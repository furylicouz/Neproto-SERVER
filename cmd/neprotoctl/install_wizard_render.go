package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"neproto.local/chameleon/internal/buildinfo"
)

func renderInstallWizard(screen tcell.Screen, model *installWizardModel) {
	screen.SetStyle(tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground))
	screen.Clear()
	width, height := screen.Size()
	if width < 100 || height < 30 {
		renderCompactInstallWizard(screen, model, width, height)
		screen.Show()
		return
	}

	cyanBold := tcell.StyleDefault.Foreground(tuiCyan).Background(tuiBackground).Bold(true)
	muted := tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground)
	putTUIText(screen, 1, 0, 12, time.Now().Format("15:04:05"), cyanBold)
	putTUICentered(screen, 0, width, "NEPROTO // CONSTELLATION DEPLOYMENT", cyanBold)
	putTUIRight(screen, width-1, 0, "INSTALLER "+buildinfo.Version, muted)
	putTUICentered(screen, 2, width, "[01 MODE] [02 DOMAIN] [03 WEB] [04 IDENTITY] [05 REVIEW] [06 DEPLOY] [07 VERIFY]", muted)
	drawTUIHorizontal(screen, 0, width-1, 3, tuiDimCyan)

	bodyTop, bodyBottom := 4, height-8
	leftWidth := maxInt(27, width*23/100)
	rightWidth := maxInt(29, width*24/100)
	centerLeft := leftWidth + 1
	rightLeft := width - rightWidth
	drawTUIBox(screen, 0, bodyTop, leftWidth, bodyBottom, " INSTALLATION MATRIX ", tuiCyan)
	drawTUIBox(screen, centerLeft, bodyTop, rightLeft-1, bodyBottom, " DEPLOYMENT TERMINAL ", tuiCyan)
	drawTUIBox(screen, rightLeft, bodyTop, width-1, bodyBottom, " NETWORK MAP ", tuiCyan)
	renderInstallMatrix(screen, model, 0, bodyTop, leftWidth, bodyBottom)
	renderInstallCenter(screen, model, centerLeft, bodyTop, rightLeft-1, bodyBottom)
	renderInstallMap(screen, model, rightLeft, bodyTop, width-1, bodyBottom)
	renderInstallFooterPanel(screen, model, 0, height-7, width-1, height-2)
	putTUIText(screen, 1, height-1, width-2, installWizardFooter(model), muted)
	screen.Show()
}

func renderCompactInstallWizard(screen tcell.Screen, model *installWizardModel, width, height int) {
	cyan := tcell.StyleDefault.Foreground(tuiCyan).Background(tuiBackground).Bold(true)
	putTUICentered(screen, 0, width, "NEPROTO INSTALLER", cyan)
	putTUICentered(screen, 2, width, "Resize terminal to at least 100x30 for cinematic mode", tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground))
	if width > 4 && height > 7 {
		drawTUIBox(screen, 1, 4, width-2, height-3, " DEPLOYMENT ", tuiDimCyan)
		lines := installWizardCenterLines(model, width-8, maxInt(1, height-10))
		for index, line := range lines {
			putTUIText(screen, 3, 6+index, width-6, line, tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground))
		}
	}
	putTUIText(screen, 2, height-1, width-4, installWizardFooter(model), tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground))
}

func renderInstallMatrix(screen tcell.Screen, model *installWizardModel, left, top, right, bottom int) {
	x, y, width := left+2, top+2, right-left-3
	stages := []struct {
		name  string
		stage installStage
	}{
		{"DEPLOYMENT MODE", installStageMode}, {"PUBLIC DOMAIN", installStageDomain},
		{"WEB PUBLICATION", installStageWebDomain},
		{"ACME IDENTITY", installStageEmail}, {"REVIEW PLAN", installStageConfirm},
		{"APPLY TRANSACTION", installStageRunning}, {"VERIFY SERVICES", installStageDone},
	}
	for index, item := range stages {
		state := "PENDING"
		style := tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground)
		if model.stage > item.stage || model.stage == installStageDone {
			state = "DONE"
			style = style.Foreground(tuiGreen)
		} else if model.stage == item.stage || (model.stage == installStageFailed && item.stage == installStageRunning) {
			state = "ACTIVE"
			style = style.Foreground(tuiCyan).Bold(true)
		}
		putTUIText(screen, x, y+index*2, width, fmt.Sprintf("%02d %-18s", index+1, item.name), style)
		putTUIRight(screen, right-2, y+index*2, state, style)
	}
	y += len(stages)*2 + 1
	putTUIText(screen, x, y, width, "TRANSACTION PROGRESS", tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground))
	y++
	renderTUIProgress(screen, x, y, width, uint64(model.progress))
	y++
	putTUIRight(screen, right-2, y, fmt.Sprintf("%d%%", model.progress), tcell.StyleDefault.Foreground(tuiGreen).Background(tuiBackground).Bold(true))
	if model.startedAt.IsZero() || y+3 >= bottom {
		return
	}
	putTUIText(screen, x, y+2, width, "ELAPSED  "+time.Since(model.startedAt).Round(time.Second).String(), tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground))
}

func renderInstallCenter(screen tcell.Screen, model *installWizardModel, left, top, right, bottom int) {
	x, y, width := left+2, top+2, right-left-3
	lines := installWizardCenterLines(model, width, maxInt(1, bottom-y-2))
	for index, line := range lines {
		style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiBackground)
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "FAILED") || strings.HasPrefix(upper, "ERROR") {
			style = style.Foreground(tuiAmber).Bold(true)
		} else if strings.HasPrefix(upper, "SUCCESS") || strings.Contains(upper, "VERIFIED") {
			style = style.Foreground(tuiGreen).Bold(true)
		} else if strings.HasPrefix(line, ">") || strings.HasPrefix(line, "[") {
			style = style.Foreground(tuiCyan)
		}
		putTUIText(screen, x, y+index, width, line, style)
	}
	if model.errorText != "" {
		putTUIText(screen, x, bottom-2, width, "ERROR // "+model.errorText, tcell.StyleDefault.Foreground(tuiAmber).Background(tuiBackground).Bold(true))
	}
}

func installWizardCenterLines(model *installWizardModel, width, height int) []string {
	var lines []string
	switch model.stage {
	case installStageMode:
		lines = []string{
			"SELECT DEPLOYMENT ENGINE", "",
			installChoice(model.mode == "bare-metal", "BARE METAL", "Native systemd services, direct host performance"),
			installChoice(model.mode == "docker", "DOCKER", "Isolated Compose stack and reproducible runtime"),
			"", "Use arrows to select. Press Enter to continue.",
		}
	case installStageDomain:
		lines = installInputLines("PUBLIC SERVER IDENTITY", "Lowercase DNS name pointed at this VPS", model.input, "vpn.example.com")
	case installStageWebDomain:
		lines = installInputLines("WEB ADMIN PUBLICATION", "Optional separate DNS name; leave empty for public TCP port 3000", model.input, "admin.example.com (optional)")
	case installStageEmail:
		lines = installInputLines("ACME CERTIFICATE IDENTITY", "Optional expiry and recovery email", model.input, "ops@example.com (optional)")
	case installStageConfirm:
		lines = []string{
			"REVIEW PRODUCTION PLAN", "", "DEPLOYMENT   " + strings.ToUpper(model.mode),
			"DOMAIN       " + model.domain,
			"WEB ADMIN    " + installWebAddress(model),
			"ACME EMAIL   " + displayInstallValue(model.email, "not configured"), "",
			"The transaction installs dependencies, writes an atomic", "configuration, provisions TLS, starts NP/2, and runs doctor.", "",
			"PRESS ENTER TO DEPLOY // ESC TO EDIT",
		}
	case installStageRunning:
		lines = []string{"LIVE DEPLOYMENT STREAM // ALL OUTPUT REMAINS IN CONSOLE", ""}
		visible := maxInt(1, height-len(lines))
		start := maxInt(0, len(model.logs)-visible)
		for _, line := range model.logs[start:] {
			lines = append(lines, "> "+line)
		}
	case installStageDone:
		lines = []string{"SUCCESS // NP/2 SERVER VERIFIED", "", "Domain: " + model.domain, "Web admin: " + installWebAddress(model), "Mode: " + model.mode, "", "The management console starts automatically on interactive SSH.", "Press Enter to close the installer and enter the server shell.", "", "Manual control command: np"}
	case installStageFailed:
		lines = []string{"FAILED // TRANSACTION STOPPED", "", model.errorText, "", "Review the final log entries below, correct the cause, and rerun install.sh.", ""}
		visible := maxInt(1, height-len(lines))
		start := maxInt(0, len(model.logs)-visible)
		for _, line := range model.logs[start:] {
			lines = append(lines, "> "+line)
		}
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

func installChoice(selected bool, name, description string) string {
	prefix := "  [ ] "
	if selected {
		prefix = "> [◆] "
	}
	return prefix + name + " // " + description
}

func installInputLines(title, description, value, placeholder string) []string {
	shown := value
	if shown == "" {
		shown = placeholder
	}
	return []string{title, "", description, "", "┌────────────────────────────────────────────────────────┐", "  " + shown + "_", "└────────────────────────────────────────────────────────┘", "", "ENTER CONTINUE // ESC BACK"}
}

func renderInstallMap(screen tcell.Screen, model *installWizardModel, left, top, right, bottom int) {
	x, y, width := left+2, top+2, right-left-3
	putTUICenteredIn(screen, x, y, width, "MAPSCII // OFFLINE WORLD", tcell.StyleDefault.Foreground(tuiCyan).Background(tuiBackground).Bold(true))
	mapHeight := minInt(12, maxInt(5, bottom-top-16))
	for _, line := range renderBrailleWorldMap(width, mapHeight, tuiMapState{zoom: 1}) {
		y++
		putTUIText(screen, x, y, width, line, tcell.StyleDefault.Foreground(tuiCyan).Background(tuiBackground))
	}
	y += 2
	rows := []string{"TARGET", displayInstallValue(model.domain, "awaiting domain"), "", "WEB ADMIN", installWebAddress(model), "", "MODE", strings.ToUpper(model.mode), "", "TLS", "ACME HTTP-01", "", "DATA PLANE", "NP/2 CONSTELLATION"}
	for _, row := range rows {
		if y >= bottom {
			break
		}
		putTUIText(screen, x, y, width, row, tcell.StyleDefault.Foreground(tuiMuted).Background(tuiBackground))
		y++
	}
}

func renderInstallFooterPanel(screen tcell.Screen, model *installWizardModel, left, top, right, bottom int) {
	drawTUIBox(screen, left, top, right, bottom, " DEPLOYMENT COMPONENTS ", tuiDimCyan)
	x, y, width := left+2, top+2, right-left-3
	columns := []string{"▣ NP/2 CORE", "▣ WEB ADMIN", "▣ CADDY EDGE", "▣ TLS VAULT", "▣ AUTO CONSOLE"}
	columnWidth := maxInt(1, width/len(columns))
	for index, value := range columns {
		putTUIText(screen, x+index*columnWidth, y, columnWidth-1, value, tcell.StyleDefault.Foreground(tuiCyan).Background(tuiBackground).Bold(true))
	}
}

func installWizardFooter(model *installWizardModel) string {
	switch model.stage {
	case installStageRunning:
		return "DEPLOYMENT LOCKED // DO NOT CLOSE THIS SSH SESSION"
	case installStageDone, installStageFailed:
		return "ENTER CLOSE"
	case installStageMode:
		return "ARROWS SELECT   ENTER CONTINUE   ESC CANCEL"
	default:
		return "TYPE VALUE   BACKSPACE EDIT   ENTER CONTINUE   ESC BACK"
	}
}

func displayInstallValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func installWebAddress(model *installWizardModel) string {
	if model.webDomain != "" {
		return "https://" + model.webDomain
	}
	return fmt.Sprintf("public TCP :%d", model.webPort)
}
