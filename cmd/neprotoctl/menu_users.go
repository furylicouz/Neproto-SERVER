package main

import (
	"fmt"
	"strconv"
	"strings"

	"neproto.local/chameleon/internal/admin"
	"neproto.local/chameleon/internal/onboarding"
)

func usersMenu(console menuConsole, manager *admin.Manager, controller serviceController) {
	for {
		users, err := manager.ListUsers()
		if err != nil {
			fmt.Fprintf(console.errors, "list users failed: %v\n", err)
			return
		}
		console.clear()
		fmt.Fprintln(console.output, asciiWordmark)
		fmt.Fprintln(console.output, "             USERS AND CLIENT ACCESS")
		fmt.Fprintln(console.output, strings.Repeat("=", 72))
		printUsers(console, users)
		fmt.Fprintln(console.output, strings.Repeat("-", 72))
		fmt.Fprintln(console.output, "1. Refresh user list")
		fmt.Fprintln(console.output, "2. Create user")
		fmt.Fprintln(console.output, "3. Show/export client configuration")
		fmt.Fprintln(console.output, "4. Rotate user credential")
		fmt.Fprintln(console.output, "5. Revoke user")
		fmt.Fprintln(console.output, "6. Permanently delete revoked user")
		fmt.Fprintln(console.output, "0. Back")
		selection, ok := console.readLine("Select: ")
		if !ok || selection == "0" {
			return
		}

		switch selection {
		case "1":
			continue
		case "2":
			menuAddUser(console, manager, controller)
		case "3":
			menuExportUser(console, manager, users)
		case "4":
			menuRotateUser(console, manager, controller, users)
		case "5":
			menuRevokeUser(console, manager, controller, users)
		case "6":
			menuDeleteUser(console, manager, controller, users)
		default:
			fmt.Fprintln(console.output, "Unknown menu item.")
		}
		if !console.pause() {
			return
		}
	}
}

func printUsers(console menuConsole, users []admin.User) {
	if len(users) == 0 {
		fmt.Fprintln(console.output, "No users have been created.")
		return
	}
	fmt.Fprintln(console.output, "#   STATUS   MODE          NAME                         ID")
	for index, user := range users {
		fmt.Fprintf(
			console.output,
			"%-3d %-8s %-13s %-28s %s\n",
			index+1,
			user.Status,
			"automatic",
			truncateRunes(user.Name, 28),
			user.ID,
		)
	}
}

func menuAddUser(console menuConsole, manager *admin.Manager, controller serviceController) {
	name, ok := console.readLine("User/device name: ")
	if !ok || name == "" {
		fmt.Fprintln(console.output, "Creation cancelled.")
		return
	}
	user, err := manager.AddUser(name, "web")
	if err != nil {
		fmt.Fprintf(console.errors, "create user failed: %v\n", err)
		return
	}
	if err := controller.Action("restart", console.output, console.errors); err != nil {
		fmt.Fprintf(console.errors, "user was created, but restart failed: %v\n", err)
		return
	}
	fmt.Fprintf(console.output, "Created user: %s\n", user.Name)
	fmt.Fprintf(console.output, "Credential ID: %s\n", user.ID)
	showBundle, ok := console.readLine("Display manual settings, import URI and QR now? [Y/n]: ")
	if ok && !strings.EqualFold(showBundle, "n") {
		profile, exportErr := manager.ExportUserProfile(user.ID)
		if exportErr != nil {
			fmt.Fprintf(console.errors, "export user failed: %v\n", exportErr)
			return
		}
		uri, encodeErr := onboarding.EncodeURI(profile)
		if encodeErr != nil {
			fmt.Fprintf(console.errors, "encode user profile failed: %v\n", encodeErr)
			return
		}
		writeManualProfile(console.output, profile)
		fmt.Fprintf(console.output, "Import URI: %s\n\nCLIENT QR\n", uri)
		if qrErr := runQR([]string{"-t", "ANSIUTF8", "-o", "-"}, uri, console.output, console.errors); qrErr != nil {
			fmt.Fprintf(console.errors, "terminal QR unavailable: %v\n", qrErr)
		}
	}
}

func menuExportUser(console menuConsole, manager *admin.Manager, users []admin.User) {
	user, ok := selectActiveUser(console, users, "Export user number [0=cancel]: ")
	if !ok {
		return
	}
	fmt.Fprintln(console.output, "Export: 1=terminal QR, 2=manual settings, 3=terminal URI, 4=JSON file, 5=PNG file")
	selection, ok := console.readLine("Format [1]: ")
	if !ok {
		return
	}
	format := map[string]string{"": "qr", "1": "qr", "2": "manual", "3": "uri", "4": "json", "5": "png"}[selection]
	if format == "" {
		fmt.Fprintln(console.output, "Invalid export format.")
		return
	}
	output := ""
	if format == "json" || format == "png" {
		output, ok = console.readLine("Absolute output path: ")
		if !ok || output == "" {
			fmt.Fprintln(console.output, "Export cancelled.")
			return
		}
	}
	menuExportUserByID(console, manager, user.ID, format, output)
}

func menuDeleteUser(console menuConsole, manager *admin.Manager, controller serviceController, users []admin.User) {
	user, ok := selectRevokedUser(console, users, "Delete revoked user number [0=cancel]: ")
	if !ok {
		return
	}
	confirmation, ok := console.readLine("Type DELETE to erase this user and archived credentials: ")
	if !ok || confirmation != "DELETE" {
		fmt.Fprintln(console.output, "Deletion cancelled.")
		return
	}
	if err := manager.DeleteUser(user.ID); err != nil {
		fmt.Fprintf(console.errors, "delete user failed: %v\n", err)
		return
	}
	if err := syncClusterUserCredential(manager, user.ID); err != nil {
		fmt.Fprintf(console.errors, "user deleted locally, but cluster sync failed: %v\n", err)
		return
	}
	if err := controller.Action("restart", console.output, console.errors); err != nil {
		fmt.Fprintf(console.errors, "user deleted, but restart failed: %v\n", err)
		return
	}
	fmt.Fprintf(console.output, "Permanently deleted %s.\n", user.Name)
}

func selectRevokedUser(console menuConsole, users []admin.User, prompt string) (admin.User, bool) {
	selection, ok := console.readLine(prompt)
	if !ok || selection == "" || selection == "0" {
		return admin.User{}, false
	}
	position, err := strconv.Atoi(selection)
	if err != nil || position < 1 || position > len(users) {
		fmt.Fprintln(console.output, "Invalid user number.")
		return admin.User{}, false
	}
	user := users[position-1]
	if user.Status != admin.StatusRevoked {
		fmt.Fprintln(console.output, "Revoke this user before permanent deletion.")
		return admin.User{}, false
	}
	return user, true
}

func menuExportUserByID(
	console menuConsole,
	manager *admin.Manager,
	identifier, format, output string,
) {
	uri, err := manager.ExportUserURI(identifier)
	if err != nil {
		fmt.Fprintf(console.errors, "export user failed: %v\n", err)
		return
	}
	fmt.Fprintln(console.output, "WARNING: this export grants VPN access. Show it only to the intended user.")
	if err := exportCredential(uri, format, output, console.output, console.errors); err != nil {
		fmt.Fprintf(console.errors, "export failed: %v\n", err)
		return
	}
	if output != "" {
		fmt.Fprintf(console.output, "Saved with mode 0600: %s\n", output)
	}
}

func menuRotateUser(
	console menuConsole,
	manager *admin.Manager,
	controller serviceController,
	users []admin.User,
) {
	user, ok := selectActiveUser(console, users, "Rotate user number [0=cancel]: ")
	if !ok {
		return
	}
	confirmation, ok := console.readLine("Type ROTATE to invalidate the current client configuration: ")
	if !ok || confirmation != "ROTATE" {
		fmt.Fprintln(console.output, "Rotation cancelled.")
		return
	}
	if err := manager.RotateUser(user.ID); err != nil {
		fmt.Fprintf(console.errors, "rotate user failed: %v\n", err)
		return
	}
	if err := syncClusterUserCredential(manager, user.ID); err != nil {
		fmt.Fprintf(console.errors, "credential rotated locally, but cluster sync failed: %v\n", err)
		return
	}
	if err := controller.Action("restart", console.output, console.errors); err != nil {
		fmt.Fprintf(console.errors, "credential rotated, but restart failed: %v\n", err)
		return
	}
	fmt.Fprintf(console.output, "Credential rotated for %s. Export a new QR.\n", user.Name)
}

func menuRevokeUser(
	console menuConsole,
	manager *admin.Manager,
	controller serviceController,
	users []admin.User,
) {
	user, ok := selectActiveUser(console, users, "Revoke user number [0=cancel]: ")
	if !ok {
		return
	}
	confirmation, ok := console.readLine("Type REVOKE to permanently disable this access: ")
	if !ok || confirmation != "REVOKE" {
		fmt.Fprintln(console.output, "Revocation cancelled.")
		return
	}
	if err := manager.RevokeUser(user.ID); err != nil {
		fmt.Fprintf(console.errors, "revoke user failed: %v\n", err)
		return
	}
	if err := syncClusterUserCredential(manager, user.ID); err != nil {
		fmt.Fprintf(console.errors, "credential revoked locally, but cluster sync failed: %v\n", err)
		return
	}
	if err := controller.Action("restart", console.output, console.errors); err != nil {
		fmt.Fprintf(console.errors, "credential revoked, but restart failed: %v\n", err)
		return
	}
	fmt.Fprintf(console.output, "Access revoked for %s.\n", user.Name)
}

func selectActiveUser(console menuConsole, users []admin.User, prompt string) (admin.User, bool) {
	selection, ok := console.readLine(prompt)
	if !ok || selection == "" || selection == "0" {
		return admin.User{}, false
	}
	position, err := strconv.Atoi(selection)
	if err != nil || position < 1 || position > len(users) {
		fmt.Fprintln(console.output, "Invalid user number.")
		return admin.User{}, false
	}
	user := users[position-1]
	if user.Status != admin.StatusActive {
		fmt.Fprintln(console.output, "The selected user is not active.")
		return admin.User{}, false
	}
	return user, true
}

func truncateRunes(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	if maximum <= 1 {
		return string(runes[:maximum])
	}
	return string(runes[:maximum-1]) + "…"
}
