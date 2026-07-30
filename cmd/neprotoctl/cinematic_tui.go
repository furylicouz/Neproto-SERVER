package main

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"

	"neproto.local/chameleon/internal/admin"
)

func runConstellationTUI(
	console menuConsole,
	manager *admin.Manager,
	controller serviceController,
) (int, error) {
	screen, err := tcell.NewScreen()
	if err != nil {
		return 1, fmt.Errorf("create terminal screen: %w", err)
	}
	if err := screen.Init(); err != nil {
		return 1, fmt.Errorf("initialize terminal screen: %w", err)
	}
	screen.HideCursor()

	stopRefresh := make(chan struct{})
	backgroundEvents := make(chan tuiBackgroundEvent, 32)
	var refreshLoop sync.WaitGroup
	refreshLoop.Add(1)
	go func() {
		defer refreshLoop.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = screen.PostEvent(tcell.NewEventInterrupt(nil))
			case <-stopRefresh:
				return
			}
		}
	}()
	defer func() {
		close(stopRefresh)
		refreshLoop.Wait()
		screen.Fini()
	}()

	model := constellationTUIModel{now: time.Now(), status: "SYSTEM READY", mapState: tuiMapState{zoom: 1}}
	if err := model.refresh(manager, controller, model.now, true); err != nil {
		return 1, fmt.Errorf("load dashboard state: %w", err)
	}
	renderConstellationTUI(screen, &model)

	for {
		event := screen.PollEvent()
		switch event := event.(type) {
		case *tcell.EventResize:
			screen.Sync()
			renderConstellationTUI(screen, &model)
		case *tcell.EventInterrupt:
			backgroundUpdated := drainTUIBackgroundEvents(&model, backgroundEvents)
			if err := model.refresh(manager, controller, time.Now(), false); err != nil {
				model.status = "REFRESH FAILED: " + boundedDisplay(err.Error(), 52)
			} else if model.backgroundOperation != tuiOperationNone {
				model.status = "BACKGROUND // " + boundedDisplay(model.dialog.prompt, 38)
			} else if !backgroundUpdated {
				model.status = "LIVE // " + model.now.Format("15:04:05")
			}
			renderConstellationTUI(screen, &model)
		case *tcell.EventKey:
			quit, selected := handleConstellationTUIKey(event, &model)
			if quit {
				return 0, nil
			}
			if selected {
				model.status = "WORKSPACE LOADING"
				renderConstellationTUI(screen, &model)
				quit, err := invokeConstellationTUIAction(manager, controller, &model, backgroundEvents)
				if err != nil {
					return 1, err
				}
				if quit {
					return 0, nil
				}
			}
			renderConstellationTUI(screen, &model)
		case nil:
			return 0, nil
		}
	}
}

func handleConstellationTUIKey(event *tcell.EventKey, model *constellationTUIModel) (quit, selected bool) {
	if model.backgroundOperation != tuiOperationNone {
		return false, false
	}
	if handled, invoke := handleTUIDialogKey(event, model); handled {
		return false, invoke
	}
	if model.view != tuiViewDashboard {
		if event.Key() == tcell.KeyEscape || (event.Key() == tcell.KeyRune && (event.Rune() == 'q' || event.Rune() == 'Q')) {
			model.view = tuiViewDashboard
			model.scroll = 0
			model.status = "DASHBOARD"
			return false, false
		}
		if model.view == tuiViewMap {
			return handleTUIMapKey(event, model)
		}
		if handled, invoke := handleTUIWorkspaceActionKey(event, model); handled {
			return false, invoke
		}
		switch event.Key() {
		case tcell.KeyUp:
			if model.listIndex > 0 {
				model.listIndex--
			}
		case tcell.KeyDown:
			model.listIndex++
		case tcell.KeyPgUp:
			model.scroll = maxInt(0, model.scroll-8)
		case tcell.KeyPgDn:
			model.scroll += 8
		case tcell.KeyRune:
			switch event.Rune() {
			case 'k', 'K':
				if model.listIndex > 0 {
					model.listIndex--
				}
			case 'j', 'J':
				model.listIndex++
			case 'r', 'R':
				return false, true
			}
		}
		model.clampListIndex()
		return false, false
	}
	switch event.Key() {
	case tcell.KeyCtrlC:
		return true, false
	case tcell.KeyUp:
		model.moveSelection(-1)
	case tcell.KeyDown:
		model.moveSelection(1)
	case tcell.KeyEnter:
		return model.openSelectedView(), model.view != tuiViewDashboard
	case tcell.KeyF1:
		model.selected = 0
		return model.openSelectedView(), true
	case tcell.KeyF2:
		model.selected = 1
		return model.openSelectedView(), true
	case tcell.KeyF3:
		model.selected = 2
		return model.openSelectedView(), true
	case tcell.KeyF4:
		model.selected = 3
		return model.openSelectedView(), true
	case tcell.KeyF5:
		model.selected = 4
		return model.openSelectedView(), true
	case tcell.KeyF6:
		model.selected = 5
		return model.openSelectedView(), true
	case tcell.KeyF7:
		model.selected = 6
		return model.openSelectedView(), true
	case tcell.KeyF8:
		model.selected = 7
		return model.openSelectedView(), true
	case tcell.KeyF10:
		model.selected = len(constellationTUIActions) - 1
		return true, false
	case tcell.KeyRune:
		switch event.Rune() {
		case 'q', 'Q':
			return true, false
		case 'k', 'K':
			model.moveSelection(-1)
		case 'j', 'J':
			model.moveSelection(1)
		case 'r', 'R':
			model.lastFull = time.Time{}
			model.status = "REFRESH REQUESTED"
		}
	}
	return false, false
}

func (model *constellationTUIModel) clampListIndex() {
	maximum := 0
	switch model.view {
	case tuiViewUsers:
		maximum = maxInt(0, len(model.users)-1)
	case tuiViewCluster:
		maximum = maxInt(0, len(model.clusterNodes)-1)
	case tuiViewRoutes:
		maximum = maxInt(0, len(model.clusterRoutes)-1)
	case tuiViewBackups:
		maximum = maxInt(0, len(model.backups)-1)
	}
	model.listIndex = minInt(maxInt(0, model.listIndex), maximum)
}

func invokeConstellationTUIAction(
	manager *admin.Manager,
	controller serviceController,
	model *constellationTUIModel,
	backgroundEvents chan<- tuiBackgroundEvent,
) (bool, error) {
	if model.pending.operation != tuiOperationNone {
		if isConstellationBackgroundOperation(model.pending.operation) {
			if startConstellationBackgroundOperation(manager, controller, model, backgroundEvents) {
				return false, nil
			}
			model.pending = tuiPendingOperation{}
			model.clearClusterDraft()
			model.dialog = infoDialog("ACTION FAILED", []string{"background executor unavailable; cluster enrolment was not started"})
			model.status = "ACTION FAILED"
			return false, nil
		}
		executeConstellationOperation(manager, controller, model)
		return false, nil
	}
	var output, failures bytes.Buffer
	switch model.view {
	case tuiViewStatus:
		runDoctor(manager, controller, &output, &failures)
	case tuiViewEvents:
		if err := controller.Logs(false, &output, &failures); err != nil {
			fmt.Fprintf(&failures, "read events failed: %v\n", err)
		}
	}
	model.output = splitTUIOutput(output.String()+failures.String(), 400)
	if err := model.refresh(manager, controller, time.Now(), true); err != nil {
		model.status = "ACTION COMPLETE // REFRESH FAILED"
	} else {
		model.status = "ACTION COMPLETE"
	}
	return false, nil
}

func handleTUIMapKey(event *tcell.EventKey, model *constellationTUIModel) (bool, bool) {
	step := 12 / model.mapState.zoom
	switch event.Key() {
	case tcell.KeyLeft:
		model.mapState.centerLon = normalizeLongitude(model.mapState.centerLon - step)
	case tcell.KeyRight:
		model.mapState.centerLon = normalizeLongitude(model.mapState.centerLon + step)
	case tcell.KeyUp:
		model.mapState.centerLat = mathClamp(model.mapState.centerLat+step/2, -75, 75)
	case tcell.KeyDown:
		model.mapState.centerLat = mathClamp(model.mapState.centerLat-step/2, -75, 75)
	case tcell.KeyRune:
		switch event.Rune() {
		case 'a', 'A':
			model.mapState.zoom = mathClamp(model.mapState.zoom*1.25, 1, 8)
		case 'z', 'Z':
			model.mapState.zoom = mathClamp(model.mapState.zoom/1.25, 1, 8)
		case 'c', 'C':
			model.mapState = tuiMapState{zoom: 1}
		}
	}
	return false, false
}

func mathClamp(value, minimum, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func splitTUIOutput(value string, maximum int) []string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > maximum {
		lines = lines[len(lines)-maximum:]
	}
	for index := range lines {
		lines[index] = boundedDisplay(lines[index], 512)
	}
	return lines
}
