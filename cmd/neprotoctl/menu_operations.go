package main

import (
	"fmt"
	"strconv"
	"strings"

	"neproto.local/chameleon/internal/admin"
)

func domainMenu(console menuConsole, manager *admin.Manager, controller serviceController) {
	for {
		console.clear()
		installation := manager.Installation()
		fmt.Fprintln(console.output, asciiWordmark)
		fmt.Fprintln(console.output, "               DOMAIN AND SERVER SETTINGS")
		fmt.Fprintln(console.output, strings.Repeat("=", 58))
		fmt.Fprintf(console.output, "Domain     : %s\n", installation.Domain)
		fmt.Fprintf(console.output, "Public IPs : %s\n", strings.Join(installation.ServerAddresses, ", "))
		fmt.Fprintf(console.output, "Deployment : %s\n", installation.Mode)
		fmt.Fprintf(console.output, "Constellation  : %s\n", enabledState(installation.EnableConstellation))
		fmt.Fprintf(console.output, "Forward secret : %s\n", enabledState(installation.EnableForwardSecrecy))
		fmt.Fprintln(console.output, "Private ingress routes are configured and hidden from this screen.")
		fmt.Fprintln(console.output, strings.Repeat("-", 58))
		fmt.Fprintln(console.output, "1. Change public domain")
		fmt.Fprintln(console.output, "2. Validate NP/2 and Caddy configuration")
		fmt.Fprintln(console.output, "3. Enable production continuity + forward secrecy")
		fmt.Fprintln(console.output, "4. Enable legacy compatibility mode")
		fmt.Fprintln(console.output, "0. Back")
		selection, ok := console.readLine("Select: ")
		if !ok || selection == "0" {
			return
		}
		switch selection {
		case "1":
			newDomain, ok := console.readLine("New lowercase domain: ")
			if !ok || newDomain == "" {
				fmt.Fprintln(console.output, "Domain change cancelled.")
				break
			}
			addresses, err := resolveDomainAddresses(newDomain)
			if err != nil {
				fmt.Fprintf(console.errors, "domain resolution failed: %v\n", err)
				break
			}
			fmt.Fprintf(console.output, "Resolved public addresses: %s\n", strings.Join(addresses, ", "))
			confirmation, ok := console.readLine("Type CHANGE to update the identity and invalidate old profiles: ")
			if !ok || confirmation != "CHANGE" {
				fmt.Fprintln(console.output, "Domain change cancelled.")
				break
			}
			performDomainSet(manager, controller, newDomain, addresses, console.output, console.errors)
		case "2":
			if err := controller.Validate(console.output, console.errors); err != nil {
				fmt.Fprintf(console.errors, "configuration validation failed: %v\n", err)
			} else {
				fmt.Fprintln(console.output, "NP/2 and Caddy configurations are valid.")
			}
		case "3", "4":
			production := selection == "3"
			confirmation := "PRODUCTION"
			if !production {
				confirmation = "COMPATIBILITY"
			}
			answer, ok := console.readLine("Type " + confirmation + " to apply, validate, restart, and probe: ")
			if !ok || answer != confirmation {
				fmt.Fprintln(console.output, "Feature change cancelled.")
				break
			}
			performFeatureSet(manager, controller, production, console.output, console.errors)
		default:
			fmt.Fprintln(console.output, "Unknown menu item.")
		}
		if !console.pause() {
			return
		}
	}
}

func backupMenu(console menuConsole, manager *admin.Manager, controller serviceController) {
	for {
		console.clear()
		paths, err := manager.ListBackups()
		if err != nil {
			fmt.Fprintf(console.errors, "list backups failed: %v\n", err)
			return
		}
		fmt.Fprintln(console.output, asciiWordmark)
		fmt.Fprintln(console.output, "                 BACKUPS AND RECOVERY")
		fmt.Fprintln(console.output, strings.Repeat("=", 72))
		if len(paths) == 0 {
			fmt.Fprintln(console.output, "No backups found.")
		} else {
			for position, path := range paths {
				fmt.Fprintf(console.output, "%d. %s\n", position+1, path)
			}
		}
		fmt.Fprintln(console.output, strings.Repeat("-", 72))
		fmt.Fprintln(console.output, "1. Create backup now")
		fmt.Fprintln(console.output, "2. Restore listed backup")
		fmt.Fprintln(console.output, "0. Back")
		selection, ok := console.readLine("Select: ")
		if !ok || selection == "0" {
			return
		}
		switch selection {
		case "1":
			path, err := manager.CreateBackup()
			if err != nil {
				fmt.Fprintf(console.errors, "backup failed: %v\n", err)
			} else {
				fmt.Fprintf(console.output, "Backup created: %s\n", path)
			}
		case "2":
			menuRestoreBackup(console, manager, controller, paths)
		default:
			fmt.Fprintln(console.output, "Unknown menu item.")
		}
		if !console.pause() {
			return
		}
	}
}

func menuRestoreBackup(
	console menuConsole,
	manager *admin.Manager,
	controller serviceController,
	paths []string,
) {
	if len(paths) == 0 {
		fmt.Fprintln(console.output, "No backup can be restored.")
		return
	}
	selection, ok := console.readLine("Backup number [0=cancel]: ")
	if !ok || selection == "0" || selection == "" {
		return
	}
	position, err := strconv.Atoi(selection)
	if err != nil || position < 1 || position > len(paths) {
		fmt.Fprintln(console.output, "Invalid backup number.")
		return
	}
	confirmation, ok := console.readLine("Type RESTORE to replace current domain and credentials: ")
	if !ok || confirmation != "RESTORE" {
		fmt.Fprintln(console.output, "Restore cancelled.")
		return
	}
	backupCommand(
		manager,
		controller,
		[]string{"restore", "--path", paths[position-1], "--confirm", "RESTORE"},
		console.output,
		console.errors,
	)
}

func printCommandHelp(writer interface{ Write([]byte) (int, error) }) {
	fmt.Fprintln(writer, asciiWordmark)
	fmt.Fprintln(writer, "Commands:")
	fmt.Fprintln(writer, "  np                         open this dashboard")
	fmt.Fprintln(writer, "  neprotoctl doctor          production diagnostics")
	fmt.Fprintln(writer, "  neprotoctl user ...        user and client access")
	fmt.Fprintln(writer, "  neprotoctl service ...     start/stop/restart")
	fmt.Fprintln(writer, "  neprotoctl logs --follow   live logs")
	fmt.Fprintln(writer, "  neprotoctl domain set ...  change public identity")
	fmt.Fprintln(writer, "  neprotoctl feature set ... switch production/compatibility policy")
	fmt.Fprintln(writer, "  neprotoctl backup ...      create/list/restore")
}
