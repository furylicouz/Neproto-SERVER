package main

import (
	"fmt"
	"strings"
)

func serviceMenu(console menuConsole, controller serviceController) {
	for {
		console.clear()
		snapshot := controller.Snapshot()
		fmt.Fprintln(console.output, asciiWordmark)
		fmt.Fprintln(console.output, "              SERVICE CONTROL AND LOGS")
		fmt.Fprintln(console.output, strings.Repeat("=", 58))
		fmt.Fprintf(console.output, "NP/2 service : %s\n", displayState(snapshot.NP2))
		fmt.Fprintf(console.output, "Web admin    : %s\n", displayState(snapshot.Web))
		fmt.Fprintf(console.output, "Caddy ingress: %s\n", displayState(snapshot.Ingress))
		fmt.Fprintln(console.output, strings.Repeat("-", 58))
		fmt.Fprintln(console.output, "1. Detailed service status")
		fmt.Fprintln(console.output, "2. Start all services")
		fmt.Fprintln(console.output, "3. Stop all services")
		fmt.Fprintln(console.output, "4. Restart all services")
		fmt.Fprintln(console.output, "5. Show last 200 log lines")
		fmt.Fprintln(console.output, "6. Follow live logs (Ctrl+C to leave)")
		fmt.Fprintln(console.output, "0. Back")
		selection, ok := console.readLine("Select: ")
		if !ok || selection == "0" {
			return
		}

		switch selection {
		case "1":
			runServiceAction(console, controller, "status", "")
		case "2":
			runServiceAction(console, controller, "start", "Server services started.")
		case "3":
			confirmation, confirmed := console.readLine("Type STOP to stop VPN and ingress: ")
			if confirmed && confirmation == "STOP" {
				runServiceAction(console, controller, "stop", "Server services stopped.")
			} else {
				fmt.Fprintln(console.output, "Stop cancelled.")
			}
		case "4":
			runServiceAction(console, controller, "restart", "Server services restarted.")
		case "5":
			if err := controller.Logs(false, console.output, console.errors); err != nil {
				fmt.Fprintf(console.errors, "logs failed: %v\n", err)
			}
		case "6":
			if err := controller.Logs(true, console.output, console.errors); err != nil {
				fmt.Fprintf(console.errors, "live logs stopped: %v\n", err)
			}
		default:
			fmt.Fprintln(console.output, "Unknown menu item.")
		}
		if !console.pause() {
			return
		}
	}
}

func runServiceAction(
	console menuConsole,
	controller serviceController,
	action, successMessage string,
) {
	if err := controller.Action(action, console.output, console.errors); err != nil {
		fmt.Fprintf(console.errors, "service %s failed: %v\n", action, err)
		return
	}
	if successMessage != "" {
		fmt.Fprintln(console.output, successMessage)
	}
}
