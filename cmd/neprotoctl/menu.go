package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/buildinfo"
)

const asciiWordmark = ` _   _      ____            _
| \ | | ___|  _ \ _ __ ___ | |_ ___
|  \| |/ _ \ |_) | '__/ _ \| __/ _ \
| |\  |  __/  __/| | | (_) | || (_) |
|_| \_|\___|_|   |_|  \___/ \__\___/`

type menuConsole struct {
	input  *bufio.Reader
	output io.Writer
	errors io.Writer
	ansi   bool
}

func menu(root string, stdin io.Reader, stdout, stderr io.Writer, controller serviceController) int {
	manager, err := admin.Open(root, nil, nil)
	if err != nil {
		fmt.Fprintf(stderr, "cannot open NP/2 installation: %v\n", err)
		return 1
	}
	if controller == nil {
		controller = commandController{mode: manager.Installation().Mode}
	}
	console := menuConsole{
		input: bufio.NewReaderSize(stdin, 4096), output: stdout, errors: stderr,
		ansi: terminalOutput(stdout),
	}
	if console.ansi && terminalInput(stdin) && os.Getenv("NEPROTO_CLASSIC_UI") == "" {
		code, tuiErr := runConstellationTUI(console, manager, controller)
		if tuiErr == nil {
			return code
		}
		fmt.Fprintf(stderr, "full-screen console unavailable: %v; using compatibility menu\n", tuiErr)
	}

	for {
		if err := renderDashboard(console, manager, controller.Snapshot()); err != nil {
			fmt.Fprintf(stderr, "dashboard failed: %v\n", err)
			return 1
		}
		selection, ok := console.readLine("Select: ")
		if !ok || selection == "0" {
			fmt.Fprintln(stdout, "NeProto manager closed.")
			return 0
		}
		switch selection {
		case "1":
			runDoctor(manager, controller, stdout, stderr)
		case "2":
			usersMenu(console, manager, controller)
			continue
		case "3":
			serviceMenu(console, controller)
			continue
		case "4":
			domainMenu(console, manager, controller)
			continue
		case "5":
			backupMenu(console, manager, controller)
			continue
		case "6":
			printCommandHelp(console.output)
		case "7":
			storageMenu(console, manager, controller)
			continue
		default:
			fmt.Fprintln(stdout, "Unknown menu item.")
		}
		if _, ok := console.readLine("Press Enter to continue: "); !ok {
			return 0
		}
	}
}

func (console menuConsole) clear() {
	if console.ansi {
		fmt.Fprint(console.output, "\033[2J\033[H")
	}
}

func (console menuConsole) pause() bool {
	_, ok := console.readLine("Press Enter to continue: ")
	return ok
}

func renderDashboard(console menuConsole, manager *admin.Manager, snapshot serviceSnapshot) error {
	users, err := manager.ListUsers()
	if err != nil {
		return err
	}
	active, revoked := 0, 0
	for _, user := range users {
		if user.Status == admin.StatusActive {
			active++
		} else if user.Status == admin.StatusRevoked {
			revoked++
		}
	}
	installation := manager.Installation()
	backups, err := manager.ListBackups()
	if err != nil {
		return err
	}
	if console.ansi {
		fmt.Fprint(console.output, "\033[2J\033[H\033[35;1m")
	}
	fmt.Fprintln(console.output, asciiWordmark)
	if console.ansi {
		fmt.Fprint(console.output, "\033[0m")
	}
	fmt.Fprintln(console.output, "                 NeProto")
	fmt.Fprintln(console.output, "    NP/2 CONSTELLATION SERVER CONTROL")
	fmt.Fprintln(console.output, strings.Repeat("=", 56))
	fmt.Fprintf(console.output, "Version        : %s\n", buildinfo.Version)
	fmt.Fprintf(console.output, "Deployment     : %s\n", installation.Mode)
	renderConstellationPanel(console.output, installation, snapshot)
	fmt.Fprintf(console.output, "NP/2 service : %s\n", displayState(snapshot.NP2))
	fmt.Fprintf(console.output, "Web admin    : %s (%s)\n", displayState(snapshot.Web), webAdminAddress(installation))
	fmt.Fprintf(console.output, "Caddy ingress : %s\n", displayState(snapshot.Ingress))
	fmt.Fprintf(console.output, "Active users  : %d\n", active)
	fmt.Fprintf(console.output, "Revoked users : %d\n", revoked)
	fmt.Fprintf(console.output, "Constellation : %s\n", enabledState(installation.EnableConstellation))
	fmt.Fprintf(console.output, "Forward secret: %s\n", enabledState(installation.EnableForwardSecrecy))
	renderSystemTelemetry(console.output, collectLinuxHostMetrics())
	renderSafeStoragePanel(console.output, active, revoked, len(backups))
	fmt.Fprintln(console.output, strings.Repeat("-", 56))
	fmt.Fprintln(console.output, "1. Status and diagnostics")
	fmt.Fprintln(console.output, "2. Users and client QR")
	fmt.Fprintln(console.output, "3. Service control and logs")
	fmt.Fprintln(console.output, "4. Domain and server settings")
	fmt.Fprintln(console.output, "5. Backups and recovery")
	fmt.Fprintln(console.output, "6. About and command help")
	fmt.Fprintln(console.output, "7. NeProto storage and events")
	fmt.Fprintln(console.output, "0. Exit")
	return nil
}

func renderSystemTelemetry(writer io.Writer, metrics hostMetrics) {
	fmt.Fprintln(writer, "+------------------- SYSTEM TELEMETRY ------------------+")
	fmt.Fprintf(writer, "| Host    : %-44s |\n", metrics.Hostname)
	fmt.Fprintf(writer, "| Uptime  : %-20s Load  : %-14s |\n", metrics.Uptime, metrics.Load)
	fmt.Fprintf(writer, "| Memory  : %-44s |\n", metrics.Memory)
	fmt.Fprintf(writer, "| Network : RX %-16s TX %-16s |\n", metrics.NetworkRX, metrics.NetworkTX)
	fmt.Fprintln(writer, "+-------------------------------------------------------+")
}

func renderConstellationPanel(writer io.Writer, installation admin.Installation, snapshot serviceSnapshot) {
	fmt.Fprintln(writer, "+---------------- CONSTELLATION NETWORK ----------------+")
	fmt.Fprintln(writer, "|              .-\"\"\"\"-.            SERVER NODE       |")
	fmt.Fprintln(writer, "|           .-'  .--.  '-.       Domain:", installation.Domain)
	fmt.Fprintln(writer, "|          /   .' /\\ '.   \\      IP:", strings.Join(installation.ServerAddresses, ", "))
	fmt.Fprintln(writer, "|         ;   /  /  \\  \\   ;     NP/2:", displayState(snapshot.NP2))
	fmt.Fprintln(writer, "|         |   |  *   |   |     Web:", displayState(snapshot.Web))
	fmt.Fprintln(writer, "|         |   |      |   |     Edge:", displayState(snapshot.Ingress))
	fmt.Fprintln(writer, "|         ;   \\     /   ;     Fabric:", enabledState(installation.EnableConstellation))
	fmt.Fprintln(writer, "|          \\   '---'   /      Crypto:", enabledState(installation.EnableForwardSecrecy))
	fmt.Fprintln(writer, "|           '-._____.-'         Transport: HTTPS/H3/WebRTC")
	fmt.Fprintln(writer, "+-------------------------------------------------------+")
}

func renderSafeStoragePanel(writer io.Writer, active, revoked, backups int) {
	fmt.Fprintln(writer, "+-------------------- SAFE STORAGE ---------------------+")
	fmt.Fprintln(writer, "| [CONFIG]       /etc/neproto/server.json       READY   |")
	fmt.Fprintf(writer, "| [USERS]        active=%-4d revoked=%-4d       MANAGED |\n", active, revoked)
	fmt.Fprintln(writer, "| [CERTIFICATES] /etc/neproto/tls               LOCKED  |")
	fmt.Fprintf(writer, "| [BACKUPS]      /var/backups/neproto           %-7d |\n", backups)
	fmt.Fprintln(writer, "| [EVENTS]       service journal                READ-ONLY|")
	fmt.Fprintln(writer, "+-------------------------------------------------------+")
}

func (console menuConsole) readLine(prompt string) (string, bool) {
	fmt.Fprint(console.output, prompt)
	line, err := console.input.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = console.input.ReadSlice('\n')
		}
		fmt.Fprintln(console.errors, "input is too long")
		return "", true
	}
	if err != nil && len(line) == 0 {
		return "", false
	}
	return strings.TrimSpace(string(line)), true
}

func terminalOutput(writer io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func terminalInput(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func displayState(state string) string {
	state = strings.TrimSpace(state)
	if state == "" {
		return "unknown"
	}
	return state
}

func enabledState(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func (controller commandController) Snapshot() serviceSnapshot {
	if controller.mode == admin.ModeDocker {
		return dockerSnapshot()
	}
	return serviceSnapshot{
		NP2:     systemdUnitState("neproto-server.service"),
		Ingress: systemdUnitState("caddy.service"),
		Web:     systemdUnitState("neproto-web.service"),
	}
}

func systemdUnitState(unit string) string {
	output, err := commandOutput("systemctl", "is-active", unit)
	state := strings.TrimSpace(output)
	if state != "" {
		return state
	}
	if err != nil {
		return "unknown"
	}
	return "inactive"
}

func dockerSnapshot() serviceSnapshot {
	output, err := commandOutput(
		"docker", "compose", "-f", "/opt/neproto/compose.yml",
		"ps", "--status", "running", "--services",
	)
	if err != nil {
		return serviceSnapshot{NP2: "unknown", Ingress: "unknown", Web: "unknown"}
	}
	running := make(map[string]bool)
	for _, service := range strings.Fields(output) {
		running[service] = true
	}
	snapshot := serviceSnapshot{NP2: "inactive", Ingress: "inactive", Web: "inactive"}
	if running["neproto"] {
		snapshot.NP2 = "active"
	}
	if running["caddy"] {
		snapshot.Ingress = "active"
	}
	if running["web"] {
		snapshot.Web = "active"
	}
	return snapshot
}

func webAdminAddress(installation admin.Installation) string {
	if !installation.WebEnabled {
		return "disabled"
	}
	if installation.WebDomain != "" {
		return "https://" + installation.WebDomain
	}
	return fmt.Sprintf("public TCP :%d", installation.WebPort)
}

var commandOutput = func(name string, arguments ...string) (string, error) {
	output, err := execCommand(name, arguments...)
	return string(output), err
}

var execCommand = func(name string, arguments ...string) ([]byte, error) {
	return exec.Command(name, arguments...).CombinedOutput()
}
