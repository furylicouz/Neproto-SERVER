package main

import (
	"math"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"neproto.local/chameleon/internal/cluster"
)

// The low-resolution coastline control points are an original approximation
// used by the offline renderer. The Braille-cell rendering and navigation are
// inspired by MapSCII (MIT): https://github.com/rastapasta/mapscii
var tuiWorldPolygons = [][][]float64{
	{{-168, 71}, {-140, 69}, {-125, 57}, {-123, 48}, {-105, 30}, {-96, 19}, {-82, 24}, {-80, 30}, {-65, 44}, {-53, 48}, {-60, 58}, {-80, 62}, {-98, 72}, {-130, 72}, {-168, 71}},
	{{-81, 12}, {-70, 10}, {-55, 5}, {-48, -5}, {-52, -20}, {-62, -38}, {-70, -55}, {-76, -40}, {-79, -18}, {-81, 12}},
	{{-52, 60}, {-42, 72}, {-22, 81}, {-17, 72}, {-30, 60}, {-52, 60}},
	{{-10, 36}, {-17, 20}, {-10, 5}, {5, -5}, {15, -35}, {30, -34}, {42, -12}, {51, 12}, {43, 31}, {31, 32}, {20, 37}, {-10, 36}},
	{{-10, 36}, {-8, 55}, {8, 71}, {30, 70}, {45, 60}, {70, 55}, {95, 72}, {150, 63}, {170, 52}, {145, 42}, {125, 34}, {105, 20}, {78, 8}, {68, 24}, {45, 30}, {35, 42}, {20, 40}, {-10, 36}},
	{{112, -11}, {130, -12}, {153, -28}, {146, -42}, {122, -35}, {112, -11}},
	{{48, -13}, {51, -25}, {45, -15}, {48, -13}},
	{{130, 32}, {142, 46}, {145, 38}, {130, 32}},
}

func renderBrailleWorldMap(width, height int, state tuiMapState) []string {
	return renderBrailleWorldGeometry(width, height, state, true)
}

func renderBrailleWorldGeometry(width, height int, state tuiMapState, markCenter bool) []string {
	if width < 1 || height < 1 {
		return nil
	}
	if state.zoom < 1 {
		state.zoom = 1
	}
	pixelWidth, pixelHeight := width*2, height*4
	pixels := make([]bool, pixelWidth*pixelHeight)
	project := func(lon, lat float64) (int, int, bool) {
		lonSpan, latSpan := 360/state.zoom, 180/state.zoom
		deltaLon := normalizeLongitude(lon - state.centerLon)
		deltaLat := lat - state.centerLat
		if math.Abs(deltaLon) > lonSpan/2 || math.Abs(deltaLat) > latSpan/2 {
			return 0, 0, false
		}
		x := int((deltaLon/lonSpan + .5) * float64(pixelWidth-1))
		y := int((.5 - deltaLat/latSpan) * float64(pixelHeight-1))
		return x, y, x >= 0 && x < pixelWidth && y >= 0 && y < pixelHeight
	}
	for _, polygon := range tuiWorldPolygons {
		for index := 1; index < len(polygon); index++ {
			x0, y0, ok0 := project(polygon[index-1][0], polygon[index-1][1])
			x1, y1, ok1 := project(polygon[index][0], polygon[index][1])
			if ok0 && ok1 && absInt(x1-x0) < pixelWidth/2 {
				drawTUILine(pixels, pixelWidth, pixelHeight, x0, y0, x1, y1)
			}
		}
	}
	lines := make([]string, height)
	for cellY := range height {
		var line strings.Builder
		line.Grow(width * 3)
		for cellX := range width {
			mask := brailleMask(pixels, pixelWidth, pixelHeight, cellX, cellY)
			if mask == 0 {
				line.WriteRune(' ')
			} else {
				line.WriteRune(rune(0x2800 + mask))
			}
		}
		lines[cellY] = line.String()
	}
	if markCenter {
		markerX, markerY := width/2, height/2
		lineRunes := []rune(lines[markerY])
		if markerX >= 0 && markerX < len(lineRunes) {
			lineRunes[markerX] = '◆'
			lines[markerY] = string(lineRunes)
		}
	}
	return lines
}

type tuiMapTone uint8

const (
	tuiMapCoast tuiMapTone = iota
	tuiMapCountry
	tuiMapLink
	tuiMapPulse
	tuiMapNode
	tuiMapMaster
	tuiMapUnavailable
)

type tuiMapCell struct {
	character rune
	tone      tuiMapTone
}

type tuiLocatedNode struct {
	node     cluster.Node
	location tuiMapLocation
}

func renderTUIBrailleNetworkMap(
	screen tcell.Screen,
	x, y, width, height int,
	state tuiMapState,
	nodes []cluster.Node,
	health map[string]clusterNodeHealth,
	now time.Time,
	detailed bool,
) {
	if width < 1 || height < 1 {
		return
	}
	frame := make([][]tuiMapCell, height)
	for row, line := range renderBrailleWorldGeometry(width, height, state, false) {
		frame[row] = make([]tuiMapCell, width)
		for column, character := range []rune(line) {
			if column >= width {
				break
			}
			frame[row][column] = tuiMapCell{character: character, tone: tuiMapCoast}
		}
	}
	projection := newTUIMapProjection(width, height, state)
	renderTUICountryLabels(frame, projection, state.zoom, detailed)
	located := locateTUIClusterNodes(nodes)
	renderTUIClusterLinks(frame, projection, located, health, now)
	renderTUIClusterNodes(frame, projection, located, health, detailed)
	for row := range frame {
		for column, cell := range frame[row] {
			if cell.character == 0 || cell.character == ' ' {
				continue
			}
			screen.SetContent(x+column, y+row, cell.character, nil, tuiMapCellStyle(cell.tone))
		}
	}
}

type tuiMapProjection struct {
	width, height int
	state         tuiMapState
}

func newTUIMapProjection(width, height int, state tuiMapState) tuiMapProjection {
	if state.zoom < 1 {
		state.zoom = 1
	}
	return tuiMapProjection{width: width, height: height, state: state}
}

func (projection tuiMapProjection) project(longitude, latitude float64) (int, int, bool) {
	lonSpan, latSpan := 360/projection.state.zoom, 180/projection.state.zoom
	deltaLon := normalizeLongitude(longitude - projection.state.centerLon)
	deltaLat := latitude - projection.state.centerLat
	if math.Abs(deltaLon) > lonSpan/2 || math.Abs(deltaLat) > latSpan/2 {
		return 0, 0, false
	}
	x := int((deltaLon/lonSpan + .5) * float64(projection.width-1))
	y := int((.5 - deltaLat/latSpan) * float64(projection.height-1))
	return x, y, x >= 0 && x < projection.width && y >= 0 && y < projection.height
}

func renderTUICountryLabels(frame [][]tuiMapCell, projection tuiMapProjection, zoom float64, detailed bool) {
	maximumPriority := 1
	if detailed && projection.width >= 48 {
		maximumPriority = 2
	}
	if detailed && (projection.width >= 68 || zoom >= 1.8) {
		maximumPriority = 3
	}
	if zoom >= 3 {
		maximumPriority = 4
	}
	for _, country := range tuiMapCountries {
		if country.priority > maximumPriority {
			continue
		}
		x, y, visible := projection.project(country.longitude, country.latitude)
		if !visible {
			continue
		}
		writeTUIMapText(frame, x-1, y, country.code, tuiMapCountry, false)
	}
}

func locateTUIClusterNodes(nodes []cluster.Node) []tuiLocatedNode {
	located := make([]tuiLocatedNode, 0, len(nodes))
	for _, node := range nodes {
		location, ok := locateTUIClusterNode(node)
		if ok {
			located = append(located, tuiLocatedNode{node: node, location: location})
		}
	}
	return located
}

func renderTUIClusterLinks(
	frame [][]tuiMapCell,
	projection tuiMapProjection,
	nodes []tuiLocatedNode,
	health map[string]clusterNodeHealth,
	now time.Time,
) {
	masterIndex := -1
	for index, located := range nodes {
		if tuiNodeHasRole(located.node, cluster.RoleMaster) {
			masterIndex = index
			break
		}
	}
	if masterIndex < 0 {
		return
	}
	masterX, masterY, masterVisible := projection.project(nodes[masterIndex].location.longitude, nodes[masterIndex].location.latitude)
	if !masterVisible {
		return
	}
	for index, located := range nodes {
		if index == masterIndex {
			continue
		}
		edgeX, edgeY, edgeVisible := projection.project(located.location.longitude, located.location.latitude)
		if !edgeVisible || absInt(edgeX-masterX) >= projection.width/2 {
			continue
		}
		tone := tuiMapLink
		if !located.node.Enabled || health[located.node.ID].status != "UP" {
			tone = tuiMapUnavailable
		}
		drawTUIMapLink(frame, masterX, masterY, edgeX, edgeY, tone)
		if tone == tuiMapLink {
			phase := float64((now.UnixMilli()+int64(index*370))%3000) / 3000
			pulseX := masterX + int(math.Round(float64(edgeX-masterX)*phase))
			pulseY := masterY + int(math.Round(float64(edgeY-masterY)*phase))
			setTUIMapCell(frame, pulseX, pulseY, '•', tuiMapPulse, true)
		}
	}
}

func renderTUIClusterNodes(
	frame [][]tuiMapCell,
	projection tuiMapProjection,
	nodes []tuiLocatedNode,
	health map[string]clusterNodeHealth,
	detailed bool,
) {
	for _, located := range nodes {
		x, y, visible := projection.project(located.location.longitude, located.location.latitude)
		if !visible {
			continue
		}
		character, tone := '●', tuiMapNode
		if tuiNodeHasRole(located.node, cluster.RoleMaster) {
			character, tone = '◆', tuiMapMaster
		}
		if !located.node.Enabled {
			character, tone = '◇', tuiMapUnavailable
		} else if health[located.node.ID].status == "DOWN" {
			character, tone = '×', tuiMapUnavailable
		} else if health[located.node.ID].status != "UP" {
			character, tone = '○', tuiMapUnavailable
		}
		setTUIMapCell(frame, x, y, character, tone, true)
		label := located.location.code
		if detailed && projection.width >= 44 {
			label += " " + truncateRunes(located.node.Name, 12)
		}
		writeTUIMapText(frame, x+2, y, label, tone, true)
	}
}

func drawTUIMapLink(frame [][]tuiMapCell, x0, y0, x1, y1 int, tone tuiMapTone) {
	dx, sx := absInt(x1-x0), -1
	if x0 < x1 {
		sx = 1
	}
	dy, sy := -absInt(y1-y0), -1
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		setTUIMapCell(frame, x0, y0, '·', tone, true)
		if x0 == x1 && y0 == y1 {
			return
		}
		twice := 2 * err
		if twice >= dy {
			err += dy
			x0 += sx
		}
		if twice <= dx {
			err += dx
			y0 += sy
		}
	}
}

func writeTUIMapText(frame [][]tuiMapCell, x, y int, value string, tone tuiMapTone, overwrite bool) {
	for offset, character := range []rune(value) {
		setTUIMapCell(frame, x+offset, y, character, tone, overwrite)
	}
}

func setTUIMapCell(frame [][]tuiMapCell, x, y int, character rune, tone tuiMapTone, overwrite bool) {
	if y < 0 || y >= len(frame) || x < 0 || x >= len(frame[y]) {
		return
	}
	if !overwrite && frame[y][x].character != 0 && frame[y][x].character != ' ' {
		return
	}
	frame[y][x] = tuiMapCell{character: character, tone: tone}
}

func tuiNodeHasRole(node cluster.Node, role cluster.NodeRole) bool {
	for _, candidate := range node.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func tuiMapCellStyle(tone tuiMapTone) tcell.Style {
	color := tuiDimCyan
	switch tone {
	case tuiMapCountry:
		color = tuiMuted
	case tuiMapLink:
		color = tuiMagenta
	case tuiMapPulse:
		color = tuiAmber
	case tuiMapNode:
		color = tuiGreen
	case tuiMapMaster:
		color = tuiCyan
	case tuiMapUnavailable:
		color = tuiAmber
	}
	return tcell.StyleDefault.Foreground(color).Background(tuiBackground)
}

func drawTUILine(pixels []bool, width, height, x0, y0, x1, y1 int) {
	dx, sx := absInt(x1-x0), -1
	if x0 < x1 {
		sx = 1
	}
	dy, sy := -absInt(y1-y0), -1
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		if x0 >= 0 && x0 < width && y0 >= 0 && y0 < height {
			pixels[y0*width+x0] = true
		}
		if x0 == x1 && y0 == y1 {
			return
		}
		twice := 2 * err
		if twice >= dy {
			err += dy
			x0 += sx
		}
		if twice <= dx {
			err += dx
			y0 += sy
		}
	}
}

func brailleMask(pixels []bool, width, height, cellX, cellY int) int {
	dots := [4][2]int{{1, 8}, {2, 16}, {4, 32}, {64, 128}}
	mask := 0
	for y := 0; y < 4; y++ {
		for x := 0; x < 2; x++ {
			pixelX, pixelY := cellX*2+x, cellY*4+y
			if pixelX < width && pixelY < height && pixels[pixelY*width+pixelX] {
				mask |= dots[y][x]
			}
		}
	}
	return mask
}

func normalizeLongitude(value float64) float64 {
	for value > 180 {
		value -= 360
	}
	for value < -180 {
		value += 360
	}
	return value
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
