package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

func renderTUIDialog(screen tcell.Screen, model *constellationTUIModel) {
	dialog := model.dialog
	if dialog == nil {
		return
	}
	screenWidth, screenHeight := screen.Size()
	desiredWidth := 82
	if dialog.kind == tuiDialogInfo {
		for _, line := range dialog.lines {
			desiredWidth = maxInt(desiredWidth, len([]rune(line))+4)
		}
	}
	width := minInt(desiredWidth, maxInt(4, screenWidth-4))
	contentHeight := 7
	if dialog.kind == tuiDialogInfo {
		contentHeight = minInt(maxInt(8, len(dialog.lines)+5), maxInt(8, screenHeight-6))
	} else if dialog.kind == tuiDialogProgress {
		contentHeight = minInt(maxInt(12, len(dialog.lines)+9), maxInt(12, screenHeight-6))
	} else if dialog.kind == tuiDialogSelect || dialog.kind == tuiDialogMultiSelect {
		contentHeight = minInt(maxInt(9, len(dialog.options)+6), maxInt(9, screenHeight-6))
	}
	height := minInt(contentHeight, screenHeight-4)
	left := maxInt(0, (screenWidth-width)/2)
	top := maxInt(1, (screenHeight-height)/2)
	right := minInt(screenWidth-1, left+width)
	bottom := minInt(screenHeight-1, top+height)

	fillStyle := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiPanel)
	for y := top; y <= bottom; y++ {
		putTUIText(screen, left, y, right-left+1, padTUIText("", right-left+1), fillStyle)
	}
	drawTUIBox(screen, left, top, right, bottom, " "+dialog.title+" ", tuiMagenta)
	x, y, innerWidth := left+2, top+2, right-left-3
	if dialog.kind == tuiDialogInfo {
		visible := maxInt(1, bottom-y-2)
		maximumStart := maxInt(0, len(dialog.lines)-visible)
		start := minInt(maximumStart, maxInt(0, dialog.scroll))
		dialog.scroll = start
		for index, line := range dialog.lines[start:] {
			if index >= visible {
				break
			}
			putTUIText(screen, x, y+index, innerWidth, line, fillStyle)
		}
		footer := "ENTER/ESC CLOSE"
		if len(dialog.lines) > visible {
			footer = "UP/DOWN/PGUP/PGDN SCROLL   ENTER/ESC CLOSE"
		}
		putTUICenteredIn(screen, x, bottom-1, innerWidth, footer, tcell.StyleDefault.Foreground(tuiAmber).Background(tuiPanel).Bold(true))
		return
	}
	if dialog.kind == tuiDialogProgress {
		putTUIText(screen, x, y, innerWidth, dialog.prompt, tcell.StyleDefault.Foreground(tuiAmber).Background(tuiPanel).Bold(true))
		y += 2
		renderTUIProgress(screen, x, y, innerWidth-6, uint64(dialog.progress))
		putTUIRight(screen, right-2, y, fmt.Sprintf("%d%%", dialog.progress), tcell.StyleDefault.Foreground(tuiGreen).Background(tuiPanel).Bold(true))
		y += 2
		visible := maxInt(0, bottom-y-2)
		start := maxInt(0, len(dialog.lines)-visible)
		for index, line := range dialog.lines[start:] {
			if index >= visible {
				break
			}
			putTUIText(screen, x, y+index, innerWidth, line, fillStyle)
		}
		elapsed := time.Since(dialog.started).Round(time.Second)
		if model.now.After(dialog.started) {
			elapsed = model.now.Sub(dialog.started).Round(time.Second)
		}
		putTUICenteredIn(screen, x, bottom-1, innerWidth, "INSTALLATION CONTINUES // ELAPSED "+elapsed.String()+" // DO NOT CLOSE SSH", tcell.StyleDefault.Foreground(tuiAmber).Background(tuiPanel).Bold(true))
		return
	}
	if dialog.kind == tuiDialogSelect || dialog.kind == tuiDialogMultiSelect {
		putTUIText(screen, x, y, innerWidth, dialog.prompt, tcell.StyleDefault.Foreground(tuiAmber).Background(tuiPanel).Bold(true))
		y += 2
		visible := maxInt(1, bottom-y-2)
		start := 0
		if dialog.optionIndex >= visible {
			start = dialog.optionIndex - visible + 1
		}
		for row, option := range dialog.options[start:] {
			if row >= visible {
				break
			}
			index := start + row
			marker := " "
			if dialog.kind == tuiDialogMultiSelect {
				marker = "[ ]"
				if option.selected {
					marker = "[X]"
				}
			}
			line := fmt.Sprintf("%s %s", marker, option.label)
			style := tcell.StyleDefault.Foreground(tuiWhite).Background(tuiPanel)
			if index == dialog.optionIndex {
				style = style.Foreground(tuiBackground).Background(tuiCyan).Bold(true)
			}
			putTUIText(screen, x, y+row, innerWidth, padTUIText(line, innerWidth), style)
		}
		footer := "UP/DOWN SELECT   ENTER CONTINUE   ESC CANCEL"
		if dialog.kind == tuiDialogMultiSelect {
			footer = "SPACE TOGGLE   A ALL   ENTER CONTINUE   ESC CANCEL"
		}
		putTUICenteredIn(screen, x, bottom-1, innerWidth, footer, tcell.StyleDefault.Foreground(tuiMuted).Background(tuiPanel))
		return
	}
	putTUIText(screen, x, y, innerWidth, dialog.prompt, tcell.StyleDefault.Foreground(tuiAmber).Background(tuiPanel).Bold(true))
	if dialog.kind == tuiDialogText {
		y += 2
		visibleInput := dialog.input
		if dialog.secret {
			visibleInput = strings.Repeat("*", len(dialog.secretRunes))
		}
		putTUIText(screen, x, y, innerWidth, "> "+visibleInput+"_", tcell.StyleDefault.Foreground(tuiCyan).Background(tuiPanel).Bold(true))
		if dialog.operation == tuiOperationUserAdd {
			putTUIRight(screen, right-2, y, "TRAFFIC ADAPTATION AUTOMATIC", tcell.StyleDefault.Foreground(tuiGreen).Background(tuiPanel))
		}
	}
	footer := "ENTER APPLY   ESC CANCEL"
	if dialog.kind == tuiDialogConfirm {
		footer = "ENTER CONFIRM   ESC CANCEL"
	}
	putTUICenteredIn(screen, x, bottom-1, innerWidth, footer, tcell.StyleDefault.Foreground(tuiMuted).Background(tuiPanel))
}
