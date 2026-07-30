package main

import (
	"fmt"
	"strings"

	"neproto.local/chameleon/internal/admin"
)

func storageMenu(console menuConsole, manager *admin.Manager, controller serviceController) {
	for {
		console.clear()
		users, err := manager.ListUsers()
		if err != nil {
			fmt.Fprintf(console.errors, "list users failed: %v\n", err)
			return
		}
		backups, err := manager.ListBackups()
		if err != nil {
			fmt.Fprintf(console.errors, "list backups failed: %v\n", err)
			return
		}
		active, revoked := managedUserCounts(users)
		fmt.Fprintln(console.output, asciiWordmark)
		fmt.Fprintln(console.output, "                 NEPROTO SAFE STORAGE")
		fmt.Fprintln(console.output, strings.Repeat("=", 72))
		fmt.Fprintln(console.output, "[CONFIG]       Validated server and ingress configuration")
		fmt.Fprintf(console.output, "[USERS]        %d active / %d revoked credentials\n", active, revoked)
		fmt.Fprintln(console.output, "[CERTIFICATES] Managed TLS material (contents never displayed)")
		fmt.Fprintf(console.output, "[BACKUPS]      %d verified recovery snapshots\n", len(backups))
		fmt.Fprintln(console.output, "[EVENTS]       Sanitized NP/2 and ingress service journal")
		fmt.Fprintln(console.output, strings.Repeat("-", 72))
		fmt.Fprintln(console.output, "1. Validate configuration")
		fmt.Fprintln(console.output, "2. Open managed users")
		fmt.Fprintln(console.output, "3. Open backups and recovery")
		fmt.Fprintln(console.output, "4. View recent sanitized events")
		fmt.Fprintln(console.output, "0. Back")
		selection, ok := console.readLine("Select: ")
		if !ok || selection == "0" {
			return
		}
		switch selection {
		case "1":
			if err := controller.Validate(console.output, console.errors); err != nil {
				fmt.Fprintf(console.errors, "configuration validation failed: %v\n", err)
			} else {
				fmt.Fprintln(console.output, "Managed configuration is valid.")
			}
		case "2":
			usersMenu(console, manager, controller)
			continue
		case "3":
			backupMenu(console, manager, controller)
			continue
		case "4":
			if err := controller.Logs(false, console.output, console.errors); err != nil {
				fmt.Fprintf(console.errors, "read events failed: %v\n", err)
			}
		default:
			fmt.Fprintln(console.output, "Unknown menu item.")
		}
		if !console.pause() {
			return
		}
	}
}

func managedUserCounts(users []admin.User) (active, revoked int) {
	for _, user := range users {
		switch user.Status {
		case admin.StatusActive:
			active++
		case admin.StatusRevoked:
			revoked++
		}
	}
	return active, revoked
}
