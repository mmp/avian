// panes.go
// Copyright(c) 2022 Matt Pharr, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mmp/imgui-go/v4"
)

// Panes (should) mostly operate in window coordinates: (0,0) is lower
// left, just in their own pane, oblivious to the full window size.  Higher
// level code will handle positioning the panes in the main window.
type Pane interface {
	Name() string

	Duplicate(nameAsCopy bool) Pane

	Activate()
	Deactivate()

	CanTakeKeyboardFocus() bool

	Draw(ctx *PaneContext, cb *CommandBuffer)
}

type PaneUIDrawer interface {
	DrawUI()
}

type PaneContext struct {
	paneExtent       Extent2D
	parentPaneExtent Extent2D

	thumbnail bool
	platform  Platform
	cs        *ColorScheme
	mouse     *MouseState
	keyboard  *KeyboardState
	haveFocus bool
	events    *EventStream
}

type MouseState struct {
	Pos           [2]float32
	Down          [MouseButtonCount]bool
	Clicked       [MouseButtonCount]bool
	Released      [MouseButtonCount]bool
	DoubleClicked [MouseButtonCount]bool
	Dragging      [MouseButtonCount]bool
	DragDelta     [2]float32
	Wheel         [2]float32
}

const (
	MouseButtonPrimary   = 0
	MouseButtonSecondary = 1
	MouseButtonTertiary  = 2
	MouseButtonCount     = 3
)

func (ctx *PaneContext) InitializeMouse(fullDisplayExtent Extent2D) {
	ctx.mouse = &MouseState{}

	// Convert to pane coordinates:
	// imgui gives us the mouse position w.r.t. the full window, so we need
	// to subtract out displayExtent.p0 to get coordinates w.r.t. the
	// current pane.  Further, it has (0,0) in the upper left corner of the
	// window, so we need to flip y w.r.t. the full window resolution.
	pos := imgui.MousePos()
	ctx.mouse.Pos[0] = pos.X - ctx.paneExtent.p0[0]
	ctx.mouse.Pos[1] = fullDisplayExtent.p1[1] - 1 - ctx.paneExtent.p0[1] - pos.Y

	io := imgui.CurrentIO()
	wx, wy := io.MouseWheel()
	ctx.mouse.Wheel = [2]float32{wx, -wy}

	for b := 0; b < MouseButtonCount; b++ {
		ctx.mouse.Down[b] = imgui.IsMouseDown(b)
		ctx.mouse.Released[b] = imgui.IsMouseReleased(b)
		ctx.mouse.Clicked[b] = imgui.IsMouseClicked(b)
		ctx.mouse.DoubleClicked[b] = imgui.IsMouseDoubleClicked(b)
		ctx.mouse.Dragging[b] = imgui.IsMouseDragging(b, 0)
		if ctx.mouse.Dragging[b] {
			delta := imgui.MouseDragDelta(b, 0.)
			// Negate y to go to pane coordinates
			ctx.mouse.DragDelta = [2]float32{delta.X, -delta.Y}
			imgui.ResetMouseDragDelta(b)
		}
	}
}

type Key int

const (
	KeyEnter = iota
	KeyUpArrow
	KeyDownArrow
	KeyLeftArrow
	KeyRightArrow
	KeyHome
	KeyEnd
	KeyBackspace
	KeyDelete
	KeyEscape
	KeyTab
	KeyPageUp
	KeyPageDown
	KeyShift
	KeyControl
	KeyAlt
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
)

type KeyboardState struct {
	Input   string
	Pressed map[Key]interface{}
}

func NewKeyboardState() *KeyboardState {
	keyboard := &KeyboardState{Pressed: make(map[Key]interface{})}

	keyboard.Input = platform.InputCharacters()

	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyEnter)) {
		keyboard.Pressed[KeyEnter] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyDownArrow)) {
		keyboard.Pressed[KeyDownArrow] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyUpArrow)) {
		keyboard.Pressed[KeyUpArrow] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyLeftArrow)) {
		keyboard.Pressed[KeyLeftArrow] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyRightArrow)) {
		keyboard.Pressed[KeyRightArrow] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyHome)) {
		keyboard.Pressed[KeyHome] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyEnd)) {
		keyboard.Pressed[KeyEnd] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyBackspace)) {
		keyboard.Pressed[KeyBackspace] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyDelete)) {
		keyboard.Pressed[KeyDelete] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyEscape)) {
		keyboard.Pressed[KeyEscape] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyTab)) {
		keyboard.Pressed[KeyTab] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyPageUp)) {
		keyboard.Pressed[KeyPageUp] = nil
	}
	if imgui.IsKeyPressed(imgui.GetKeyIndex(imgui.KeyPageDown)) {
		keyboard.Pressed[KeyPageDown] = nil
	}
	const ImguiF1 = 290
	for i := 0; i < 12; i++ {
		if imgui.IsKeyPressed(ImguiF1 + i) {
			keyboard.Pressed[Key(int(KeyF1)+i)] = nil
		}
	}
	io := imgui.CurrentIO()
	if io.KeyShiftPressed() {
		keyboard.Pressed[KeyShift] = nil
	}
	if io.KeyCtrlPressed() {
		keyboard.Pressed[KeyControl] = nil
	}
	if io.KeyAltPressed() {
		keyboard.Pressed[KeyAlt] = nil
	}

	return keyboard
}

func (k *KeyboardState) IsPressed(key Key) bool {
	_, ok := k.Pressed[key]
	return ok
}

func (ctx *PaneContext) SetWindowCoordinateMatrices(cb *CommandBuffer) {
	w := float32(int(ctx.paneExtent.Width() + 0.5))
	h := float32(int(ctx.paneExtent.Height() + 0.5))
	cb.LoadProjectionMatrix(Identity3x3().Ortho(0, w, 0, h))
	cb.LoadModelViewMatrix(Identity3x3())
}

// Helper function to unmarshal the JSON of a Pane of a given type T.
func unmarshalPaneHelper[T Pane](data []byte) (Pane, error) {
	var p T
	err := json.Unmarshal(data, &p)
	return p, err
}

func unmarshalPane(paneType string, data []byte) (Pane, error) {
	switch paneType {
	case "":
		// nil pane
		return nil, nil

	case "*main.AirportInfoPane":
		return unmarshalPaneHelper[*AirportInfoPane](data)

	case "*main.CLIPane":
		return unmarshalPaneHelper[*CLIPane](data)

	case "*main.EmptyPane":
		return unmarshalPaneHelper[*EmptyPane](data)

	case "*main.FlightPlanPane":
		return unmarshalPaneHelper[*FlightPlanPane](data)

	case "*main.ImageViewPane":
		return unmarshalPaneHelper[*ImageViewPane](data)

	case "*main.PluginPane":
		return unmarshalPaneHelper[*PluginPane](data)

	case "*main.RadarScopePane":
		return unmarshalPaneHelper[*RadarScopePane](data)

	case "*main.TabbedPane":
		return unmarshalPaneHelper[*TabbedPane](data)

	default:
		lg.Errorf("%s: Unhandled type in config file", paneType)
		return NewEmptyPane(), nil // don't crash at least
	}
}

///////////////////////////////////////////////////////////////////////////
// AirportInfoPane

type AirportInfoPane struct {
	Airports map[string]interface{}

	ShowTime         bool
	ShowMETAR        bool
	ShowATIS         bool
	ShowApproaches   bool
	ShowRandomOnFreq bool
	ShowDepartures   bool
	ShowDeparted     bool
	ShowArrivals     bool
	ShowLanded       bool
	ShowControllers  bool

	ControllerFrequency Frequency

	lastATIS       map[string][]ATIS
	seenDepartures map[string]interface{}
	seenArrivals   map[string]interface{}

	FontIdentifier FontIdentifier
	font           *Font

	eventsId EventSubscriberId

	sb *ScrollBar
	cb CommandBuffer

	flaggedSequence map[string]int

	approaches map[string][]Approach
	// airport -> code
	activeApproaches map[string]map[string]interface{}
	drawnApproaches  map[string]map[string]interface{}
}

type ApproachFix struct {
	Fix        string
	Altitude   int
	PT, NoPT   bool
	DrawOffset [2]float32
}

func (a ApproachFix) String() string {
	s := a.Fix
	s += fmt.Sprintf("-%d", a.Altitude/100)
	if a.PT {
		s += " PT"
	}
	if a.NoPT {
		s += " NoPT"
	}
	return s
}

type ApproachFixArray []ApproachFix

func (a ApproachFixArray) String() string {
	var strs []string
	for _, ap := range a {
		strs = append(strs, ap.String())
	}

	return strings.Join(strs, "/")
}

type Approach struct {
	Runway string
	Type   string
	Code   string
	IAFs   ApproachFixArray
	IFs    ApproachFixArray
	FAF    ApproachFix
}

func NewAirportInfoPane() *AirportInfoPane {
	// Reasonable (I hope) defaults...
	return &AirportInfoPane{
		Airports:        make(map[string]interface{}),
		ShowTime:        true,
		ShowMETAR:       true,
		ShowATIS:        true,
		ShowDepartures:  true,
		ShowDeparted:    true,
		ShowArrivals:    true,
		ShowControllers: true,
	}
}

func (a *AirportInfoPane) Duplicate(nameAsCopy bool) Pane {
	dupe := *a
	dupe.Airports = DuplicateMap(a.Airports)
	dupe.lastATIS = DuplicateMap(a.lastATIS)
	dupe.seenDepartures = DuplicateMap(a.seenDepartures)
	dupe.seenArrivals = DuplicateMap(a.seenArrivals)
	dupe.eventsId = eventStream.Subscribe()
	dupe.sb = NewScrollBar(4, false)
	dupe.cb = CommandBuffer{}
	return &dupe
}

func (a *AirportInfoPane) Activate() {
	if a.font = GetFont(a.FontIdentifier); a.font == nil {
		a.font = GetDefaultFont()
		a.FontIdentifier = a.font.id
	}
	if a.lastATIS == nil {
		a.lastATIS = make(map[string][]ATIS)
	}
	if a.seenDepartures == nil {
		a.seenDepartures = make(map[string]interface{})
	}
	if a.seenArrivals == nil {
		a.seenArrivals = make(map[string]interface{})
	}
	if a.sb == nil {
		a.sb = NewScrollBar(4, false)
	}
	for ap := range a.Airports {
		server.AddAirportForWeather(ap)
	}
	a.eventsId = eventStream.Subscribe()

	if a.flaggedSequence == nil {
		a.flaggedSequence = make(map[string]int)
	}

	if a.approaches == nil {
		a.approaches = make(map[string][]Approach)
		a.activeApproaches = make(map[string]map[string]interface{})
		a.drawnApproaches = make(map[string]map[string]interface{})

		// Hardcoded, but yolo...
		a.activeApproaches["KJFK"] = make(map[string]interface{})
		a.drawnApproaches["KJFK"] = make(map[string]interface{})
		a.approaches["KJFK"] = []Approach{
			Approach{Runway: "4R", Type: "ILS", Code: "I4R",
				IFs: []ApproachFix{ApproachFix{Fix: "ZETAL", Altitude: 2000, DrawOffset: [2]float32{5, 5}}},
				FAF: ApproachFix{Fix: "EBBEE", Altitude: 1500, DrawOffset: [2]float32{5, 5}}},
			Approach{Runway: "4L", Type: "ILS", Code: "I4L",
				IFs: []ApproachFix{ApproachFix{Fix: "AROKE", Altitude: 2000, DrawOffset: [2]float32{-5, 5}}},
				FAF: ApproachFix{Fix: "KRSTL", Altitude: 1500, DrawOffset: [2]float32{-5, 10}}},
			Approach{Runway: "13L", Type: "RNAV Z", Code: "R3L",
				IFs: []ApproachFix{ApproachFix{Fix: "ASALT", Altitude: 3000, DrawOffset: [2]float32{-5, 5}}},
				FAF: ApproachFix{Fix: "CNRSE", Altitude: 2000, DrawOffset: [2]float32{-5, 5}}},
			Approach{Runway: "22L", Type: "ILS", Code: "I2L",
				IFs: []ApproachFix{ApproachFix{Fix: "ROSLY", Altitude: 3000, DrawOffset: [2]float32{5, 5}}},
				FAF: ApproachFix{Fix: "ZALPO", Altitude: 1800, DrawOffset: [2]float32{5, 5}}},
			Approach{Runway: "22L", Type: "RNAV X", Code: "R2L",
				IFs: []ApproachFix{ApproachFix{Fix: "CAPIT", Altitude: 2900, DrawOffset: [2]float32{5, 5}}},
				FAF: ApproachFix{Fix: "ENEEE", Altitude: 1700, DrawOffset: [2]float32{5, 5}}},
			Approach{Runway: "22R", Type: "ILS", Code: "I2R",
				IFs: []ApproachFix{ApproachFix{Fix: "CORVT", Altitude: 3000, DrawOffset: [2]float32{-5, 10}}},
				FAF: ApproachFix{Fix: "MATTR", Altitude: 1900, DrawOffset: [2]float32{-5, 5}}},
			Approach{Runway: "22R", Type: "RNAV", Code: "R2R",
				IFs: []ApproachFix{ApproachFix{Fix: "RIVRA", Altitude: 3000, DrawOffset: [2]float32{-5, 5}}},
				FAF: ApproachFix{Fix: "HENEB", Altitude: 1900, DrawOffset: [2]float32{-5, 5}}},
			Approach{Runway: "31R", Type: "ILS", Code: "I1R",
				IFs: []ApproachFix{ApproachFix{Fix: "CATOD", Altitude: 3000, DrawOffset: [2]float32{5, 5}}},
				FAF: ApproachFix{Fix: "ZULAB", Altitude: 1900, DrawOffset: [2]float32{5, 10}}},
			Approach{Runway: "31L", Type: "ILS", Code: "I1L",
				IFs: []ApproachFix{ApproachFix{Fix: "ZACHS", Altitude: 2000, DrawOffset: [2]float32{-5, 5}}},
				FAF: ApproachFix{Fix: "MEALS", Altitude: 1800, DrawOffset: [2]float32{-5, 5}}},
		}
		a.activeApproaches["KFRG"] = make(map[string]interface{})
		a.drawnApproaches["KFRG"] = make(map[string]interface{})
		a.approaches["KFRG"] = []Approach{
			Approach{Runway: "1", Type: "RNAV", Code: "R1",
				IAFs: []ApproachFix{ApproachFix{Fix: "ZACHS", Altitude: 2000, DrawOffset: [2]float32{-5, 5}},
					ApproachFix{Fix: "WULUG", Altitude: 2000, DrawOffset: [2]float32{5, 5}}},
				IFs: []ApproachFix{ApproachFix{Fix: "BLAND", Altitude: 2000, DrawOffset: [2]float32{-5, 0}}},
				FAF: ApproachFix{Fix: "DEUCE", Altitude: 1600, DrawOffset: [2]float32{5, 5}}},
			Approach{Runway: "14", Type: "ILS", Code: "I14",
				IFs: []ApproachFix{ApproachFix{Fix: "FRIKK", Altitude: 1600, PT: true}},
				FAF: ApproachFix{Fix: "FRIKK", Altitude: 1400}},
			Approach{Runway: "14", Type: "RNAV Z", Code: "R4Z",
				IFs: []ApproachFix{ApproachFix{Fix: "SEHDO", Altitude: 2000},
					ApproachFix{Fix: "LAAZE", Altitude: 1400}},
				FAF: ApproachFix{Fix: "ALABE", Altitude: 1400}},
			Approach{Runway: "19", Type: "RNAV", Code: "R19",
				IAFs: []ApproachFix{ApproachFix{Fix: "BLINZ", Altitude: 2000, DrawOffset: [2]float32{-5, 5}},
					ApproachFix{Fix: "ZOSAB", Altitude: 2000, DrawOffset: [2]float32{5, 5}}},
				IFs: []ApproachFix{ApproachFix{Fix: "DEBYE", Altitude: 2000, DrawOffset: [2]float32{0, 15}}},
				FAF: ApproachFix{Fix: "MOIRE", Altitude: 1500, DrawOffset: [2]float32{10, 5}}},
			Approach{Runway: "32", Type: "RNAV", Code: "R32",
				IAFs: []ApproachFix{ApproachFix{Fix: "JUSIN", Altitude: 2000, DrawOffset: [2]float32{-1, 0}},
					ApproachFix{Fix: "SHYNA", Altitude: 2000, DrawOffset: [2]float32{5, 5}}},
				IFs: []ApproachFix{ApproachFix{Fix: "TRCCY", Altitude: 2000}},
				FAF: ApproachFix{Fix: "ALFED", Altitude: 1400, DrawOffset: [2]float32{10, 5}}},
		}
		a.activeApproaches["KISP"] = make(map[string]interface{})
		a.drawnApproaches["KISP"] = make(map[string]interface{})
		a.approaches["KISP"] = []Approach{
			Approach{Runway: "6", Type: "ILS", Code: "I6",
				IFs: []ApproachFix{ApproachFix{Fix: "YOSUR", Altitude: 1600, PT: true}},
				FAF: ApproachFix{Fix: "YOSUR", Altitude: 1600}},
			Approach{Runway: "6", Type: "RNAV", Code: "R6",
				IFs: []ApproachFix{ApproachFix{Fix: "DEERY", Altitude: 2000, PT: true, DrawOffset: [2]float32{5, 5}}},
				FAF: ApproachFix{Fix: "YOSUR", Altitude: 1600, DrawOffset: [2]float32{5, 5}}},
			Approach{Runway: "15R", Type: "RNAV", Code: "R5R",
				IFs: []ApproachFix{ApproachFix{Fix: "FORMU", Altitude: 2000, PT: true, DrawOffset: [2]float32{5, 10}}},
				FAF: ApproachFix{Fix: "ZIVUX", Altitude: 1600, DrawOffset: [2]float32{5, 5}}},
			Approach{Runway: "24", Type: "ILS", Code: "I24",
				IAFs: []ApproachFix{ApproachFix{Fix: "CCC", Altitude: 2000, DrawOffset: [2]float32{5, 5}}},
				IFs:  []ApproachFix{ApproachFix{Fix: "CORAM", Altitude: 2000, DrawOffset: [2]float32{0, 15}}},
				FAF:  ApproachFix{Fix: "RIZER", Altitude: 1400}},
			Approach{Runway: "24", Type: "RNAV", Code: "R24",
				IAFs: []ApproachFix{ApproachFix{Fix: "CCC", Altitude: 2000, DrawOffset: [2]float32{5, 5}}},
				IFs:  []ApproachFix{ApproachFix{Fix: "CORAM", Altitude: 2000, DrawOffset: [2]float32{0, 15}}},
				FAF:  ApproachFix{Fix: "UKEGE", Altitude: 1400}},
			Approach{Runway: "33L", Type: "RNAV", Code: "R3L",
				IFs: []ApproachFix{ApproachFix{Fix: "BJACK", Altitude: 2000, PT: true, DrawOffset: [2]float32{5, 5}}},
				FAF: ApproachFix{Fix: "JATEV", Altitude: 1500, DrawOffset: [2]float32{5, 10}}},
		}
		a.activeApproaches["KHVN"] = make(map[string]interface{})
		a.drawnApproaches["KHVN"] = make(map[string]interface{})
		a.approaches["KHVN"] = []Approach{
			Approach{Runway: "2", Type: "ILS", Code: "I2",
				IAFs: []ApproachFix{ApproachFix{Fix: "CCC", Altitude: 1800, NoPT: true}},
				IFs: []ApproachFix{ApproachFix{Fix: "SALLT", Altitude: 1500, PT: true},
					ApproachFix{Fix: "PEPER", Altitude: 1500}},
				FAF: ApproachFix{Fix: "SALLT", Altitude: 1500}},
			Approach{Runway: "2", Type: "RNAV", Code: "R2",
				IAFs: []ApproachFix{ApproachFix{Fix: "NESSI", Altitude: 1800, DrawOffset: [2]float32{-5, 5}},
					ApproachFix{Fix: "KEYED", Altitude: 1800, NoPT: true, DrawOffset: [2]float32{5, 5}}},
				IFs: []ApproachFix{ApproachFix{Fix: "PEPER", Altitude: 1800, PT: true, DrawOffset: [2]float32{-5, -5}}},
				FAF: ApproachFix{Fix: "SALLT", Altitude: 1500, DrawOffset: [2]float32{5, 5}}},
			Approach{Runway: "20", Type: "RNAV", Code: "R20",
				IAFs: []ApproachFix{ApproachFix{Fix: "HFD", Altitude: 2600, DrawOffset: [2]float32{5, 5}},
					ApproachFix{Fix: "SORRY", Altitude: 2600, DrawOffset: [2]float32{-5, 5}}},
				IFs: []ApproachFix{ApproachFix{Fix: "GUUMP", Altitude: 2600, DrawOffset: [2]float32{5, 0}}},
				FAF: ApproachFix{Fix: "ELLVS", Altitude: 1700}},
		}
		a.activeApproaches["KBDR"] = make(map[string]interface{})
		a.drawnApproaches["KBDR"] = make(map[string]interface{})
		a.approaches["KBDR"] = []Approach{
			Approach{Runway: "24", Type: "RNAV", Code: "R24",
				IFs: []ApproachFix{ApproachFix{Fix: "DWAIN", Altitude: 2600, PT: true, DrawOffset: [2]float32{5, 5}}},
				FAF: ApproachFix{Fix: "MILUM", Altitude: 1800, DrawOffset: [2]float32{5, 5}}},
			Approach{Runway: "29", Type: "RNAV", Code: "R29",
				IAFs: []ApproachFix{ApproachFix{Fix: "CCC", Altitude: 2000, DrawOffset: [2]float32{5, 5}},
					ApproachFix{Fix: "MAD", Altitude: 2000, DrawOffset: [2]float32{5, 5}}},
				IFs: []ApproachFix{ApproachFix{Fix: "MADDG", Altitude: 2000, DrawOffset: [2]float32{5, 5}}},
				FAF: ApproachFix{Fix: "SETHE", Altitude: 1500, DrawOffset: [2]float32{5, 15}}},
			Approach{Runway: "6", Type: "ILS", Code: "I6",
				IAFs: []ApproachFix{ApproachFix{Fix: "STANE", Altitude: 1800, PT: true, DrawOffset: [2]float32{5, 5}}},
				FAF:  ApproachFix{Fix: "STANE", Altitude: 1800, DrawOffset: [2]float32{5, 5}}},
			Approach{Runway: "6", Type: "RNAV", Code: "R6",
				IFs: []ApproachFix{ApproachFix{Fix: "DABVE", Altitude: 2000, DrawOffset: [2]float32{5, 0}}},
				FAF: ApproachFix{Fix: "STANE", Altitude: 1800, DrawOffset: [2]float32{5, 0}}}}
		a.activeApproaches["KFOK"] = make(map[string]interface{})
		a.drawnApproaches["KFOK"] = make(map[string]interface{})
		a.approaches["KFOK"] = []Approach{
			Approach{Runway: "24", Type: "ILS", Code: "I24",
				IAFs: []ApproachFix{ApproachFix{Fix: "HTO", Altitude: 2700, DrawOffset: [2]float32{5, 5}}},
				IFs:  []ApproachFix{ApproachFix{Fix: "MATTY", Altitude: 2700, DrawOffset: [2]float32{5, 15}}},
				FAF:  ApproachFix{Fix: "SPREE", Altitude: 1500, DrawOffset: [2]float32{10, 5}}},
			Approach{Runway: "24", Type: "RNAV", Code: "R24",
				IAFs: []ApproachFix{ApproachFix{Fix: "HTO", Altitude: 2000, NoPT: true, DrawOffset: [2]float32{5, 5}}},
				IFs:  []ApproachFix{ApproachFix{Fix: "MATTY", Altitude: 2000, PT: true, DrawOffset: [2]float32{5, 15}}},
				FAF:  ApproachFix{Fix: "HAGIK", Altitude: 1500, DrawOffset: [2]float32{10, 5}}},
			Approach{Runway: "6", Type: "RNAV", Code: "R6",
				IFs: []ApproachFix{ApproachFix{Fix: "ZATBO", Altitude: 2600, PT: true, DrawOffset: [2]float32{5, 5}}},
				FAF: ApproachFix{Fix: "TAZZY", Altitude: 1700, DrawOffset: [2]float32{5, 5}}}}
		a.activeApproaches["KOXC"] = make(map[string]interface{})
		a.drawnApproaches["KOXC"] = make(map[string]interface{})
		a.approaches["KOXC"] = []Approach{
			Approach{Runway: "18", Type: "RNAV", Code: "R18",
				IAFs: []ApproachFix{ApproachFix{Fix: "MOONI", Altitude: 3000, NoPT: true, DrawOffset: [2]float32{-5, 5}},
					ApproachFix{Fix: "BISCO", Altitude: 3000, NoPT: true, DrawOffset: [2]float32{5, 5}}},
				IFs: []ApproachFix{ApproachFix{Fix: "WEXNO", Altitude: 3000, PT: true, DrawOffset: [2]float32{5, 15}}},
				FAF: ApproachFix{Fix: "ARQEB", Altitude: 2200, DrawOffset: [2]float32{10, 5}}},
			Approach{Runway: "36", Type: "ILS", Code: "I36",
				IAFs: []ApproachFix{ApproachFix{Fix: "BDR", Altitude: 2500, DrawOffset: [2]float32{10, 5}}},
				IFs:  []ApproachFix{ApproachFix{Fix: "CUTMA", Altitude: 2500, DrawOffset: [2]float32{5, 15}}},
				FAF:  ApproachFix{Fix: "DAAVY", Altitude: 2500, DrawOffset: [2]float32{10, 5}}},
			Approach{Runway: "36", Type: "RNAV", Code: "R36",
				IAFs: []ApproachFix{ApproachFix{Fix: "BDR", Altitude: 2500, NoPT: true, DrawOffset: [2]float32{5, 5}},
					ApproachFix{Fix: "MUVGE", Altitude: 2500, NoPT: true, DrawOffset: [2]float32{5, 5}}},
				IFs: []ApproachFix{ApproachFix{Fix: "CUTMA", Altitude: 2500, PT: true, DrawOffset: [2]float32{5, 15}}},
				FAF: ApproachFix{Fix: "DAAVY", Altitude: 2500, DrawOffset: [2]float32{10, 5}}}}
		a.activeApproaches["KJPX"] = make(map[string]interface{})
		a.drawnApproaches["KJPX"] = make(map[string]interface{})
		a.approaches["KJPX"] = []Approach{
			Approach{Runway: "10", Type: "RNAV Z", Code: "R10",
				IFs: []ApproachFix{ApproachFix{Fix: "MATHW", Altitude: 1800, PT: true, DrawOffset: [2]float32{5, 15}}},
				FAF: ApproachFix{Fix: "GOODI", Altitude: 1800, DrawOffset: [2]float32{10, 5}}},
			Approach{Runway: "28", Type: "RNAV Z", Code: "R28",
				IAFs: []ApproachFix{ApproachFix{Fix: "JORDN", Altitude: 2000, DrawOffset: [2]float32{5, 15}},
					ApproachFix{Fix: "SEFRD", Altitude: 2000, DrawOffset: [2]float32{5, 5}}},
				IFs: []ApproachFix{ApproachFix{Fix: "BIGGA", Altitude: 2000, DrawOffset: [2]float32{5, 5}}},
				FAF: ApproachFix{Fix: "FEAST", Altitude: 1900, DrawOffset: [2]float32{10, 15}}}}
	}

	// Check fixes are valid
	for icao, aps := range a.approaches {
		for _, ap := range aps {
			checkFix := func(af ApproachFix) {
				if _, ok := database.Locate(af.Fix); !ok {
					lg.Errorf("%s: fix unknown for %s approach %s", af.Fix, icao, ap.Code)
				}
			}
			for _, af := range ap.IAFs {
				checkFix(af)
			}
			for _, af := range ap.IFs {
				checkFix(af)
			}
			checkFix(ap.FAF)
		}
	}

}

func (a *AirportInfoPane) Deactivate() {
	eventStream.Unsubscribe(a.eventsId)
	a.eventsId = InvalidEventSubscriberId
}

func (a *AirportInfoPane) Name() string {
	n := "Airport Information"
	if len(a.Airports) > 0 {
		n += ": " + strings.Join(SortedMapKeys(a.Airports), ",")
	}
	return n
}

func (a *AirportInfoPane) DrawUI() {
	var changed bool
	if a.Airports, changed = drawAirportSelector(a.Airports, "Airports"); changed {
		for ap := range a.Airports {
			server.AddAirportForWeather(ap)
		}
	}
	if newFont, changed := DrawFontPicker(&a.FontIdentifier, "Font"); changed {
		a.font = newFont
	}
	imgui.Checkbox("Show time", &a.ShowTime)
	imgui.Checkbox("Show weather", &a.ShowMETAR)
	imgui.Checkbox("Show ATIS", &a.ShowATIS)
	imgui.Checkbox("Show randoms on frequency", &a.ShowRandomOnFreq)
	imgui.SameLine()
	fr := int32(a.ControllerFrequency)
	imgui.InputIntV("Frequency", &fr, 100000, 140000, 0)
	a.ControllerFrequency = Frequency(fr)
	imgui.Checkbox("Show aircraft to depart", &a.ShowDepartures)
	imgui.Checkbox("Show departed aircraft", &a.ShowDeparted)
	imgui.Checkbox("Show arriving aircraft", &a.ShowArrivals)
	imgui.Checkbox("Show landed aircraft", &a.ShowLanded)
	imgui.Checkbox("Show controllers", &a.ShowControllers)

	imgui.Separator()
	imgui.Text("Active approaches")

	maxApproaches := 0
	for _, ap := range a.approaches {
		maxApproaches = max(maxApproaches, len(ap))
	}

	flags := imgui.TableFlagsBordersH | imgui.TableFlagsBordersOuterV | imgui.TableFlagsRowBg
	if imgui.BeginTableV("##approaches", maxApproaches+1, flags, imgui.Vec2{}, 0.0) {
		for _, icao := range SortedMapKeys(a.approaches) {
			imgui.PushID(icao)
			imgui.TableNextRow()
			imgui.TableNextColumn()
			imgui.Text(icao)

			for _, ap := range a.approaches[icao] {
				imgui.TableNextColumn()
				_, active := a.activeApproaches[icao][ap.Code]
				if imgui.Checkbox(ap.Type+" "+ap.Runway, &active) {
					if active {
						a.activeApproaches[icao][ap.Code] = nil
					} else {
						delete(a.activeApproaches[icao], ap.Code)
					}
				}
			}
			imgui.PopID()
		}
		imgui.EndTable()
	}
}

type Arrival struct {
	aircraft     *Aircraft
	distance     float32
	sortDistance float32
}

func getDistanceSortedArrivals(airports map[string]interface{}) []Arrival {
	var arr []Arrival
	now := server.CurrentTime()
	for _, ac := range server.GetFilteredAircraft(func(ac *Aircraft) bool {
		if ac.OnGround() || ac.LostTrack(now) || ac.FlightPlan == nil {
			return false
		}
		_, ok := airports[ac.FlightPlan.ArrivalAirport]
		return ok
	}) {
		pos := ac.Position()
		// Filter ones where we don't have a valid position
		if pos[0] != 0 && pos[1] != 0 {
			dist := nmdistance2ll(database.airports[ac.FlightPlan.ArrivalAirport].Location, pos)
			sortDist := dist + float32(ac.Altitude())/300.
			arr = append(arr, Arrival{aircraft: ac, distance: dist, sortDistance: sortDist})
		}
	}

	sort.Slice(arr, func(i, j int) bool {
		return arr[i].sortDistance < arr[j].sortDistance
	})

	return arr
}

func (a *AirportInfoPane) CanTakeKeyboardFocus() bool { return false }

func formatAltitude(alt int) string {
	if alt >= 18000 {
		return "FL" + fmt.Sprintf("%d", alt/100)
	} else {
		th := alt / 1000
		hu := (alt % 1000) / 100 * 100
		if th == 0 {
			return fmt.Sprintf("%d", hu)
		} else if hu == 0 {
			return fmt.Sprintf("%d,000", th)
		} else {
			return fmt.Sprintf("%d,%03d", th, hu)
		}
	}
}

func (a *AirportInfoPane) Draw(ctx *PaneContext, cb *CommandBuffer) {
	for _, event := range eventStream.Get(a.eventsId) {
		switch ev := event.(type) {
		case *NewServerConnectionEvent:
			// Let the server know which airports we want weather for.
			for ap := range a.Airports {
				server.AddAirportForWeather(ap)
			}

		case *ReceivedATISEvent:
			if _, ok := a.Airports[ev.ATIS.Airport]; ok {
				newATIS := ev.ATIS
				idx := FindIf(a.lastATIS[newATIS.Airport], func(a ATIS) bool { return a.AppDep == newATIS.AppDep })
				if idx == -1 {
					// First time we've ever seen it
					a.lastATIS[newATIS.Airport] = append(a.lastATIS[newATIS.Airport], newATIS)
				} else {
					oldATIS := a.lastATIS[newATIS.Airport][idx]
					if oldATIS.Code != newATIS.Code || oldATIS.Contents != newATIS.Contents {
						// It's an updated ATIS rather than the first time we're
						// seeing it (or a repeat), so play the notification sound,
						// if it's enabled.
						a.lastATIS[newATIS.Airport][idx] = newATIS
						globalConfig.AudioSettings.HandleEvent(AudioEventUpdatedATIS)
					}
				}
			}
		}
	}

	cs := ctx.cs

	basicStyle := TextStyle{Font: a.font, Color: cs.Text}
	highlightStyle := TextStyle{Font: a.font, Color: cs.TextHighlight}
	var strs []string
	var styles []TextStyle
	nLines := 0
	lineToCallsign := make(map[int]string)
	lineToApproach := make(map[int][2]string)
	drawnFlagged := make(map[string]interface{})

	isFlagged := false
	startLine := func(callsign string) {
		nLines++
		isFlagged = positionConfig.IsFlagged(callsign)
		if isFlagged {
			drawnFlagged[callsign] = nil
		}
	}
	addText := func(style TextStyle, f string, args ...interface{}) {
		if isFlagged {
			if time.Now().Second()%2 != 0 {
				if style == highlightStyle {
					style = basicStyle
				} else {
					style = highlightStyle
				}
			}
		}
		strs = append(strs, fmt.Sprintf(f, args...))
		styles = append(styles, style)
	}
	endLine := func() {
		strs = append(strs, "\n")
		styles = append(styles, basicStyle)
	}
	emptyLine := func() {
		startLine("")
		endLine()
	}

	now := server.CurrentTime()
	if a.ShowTime {
		startLine("")
		addText(basicStyle, now.UTC().Format("Time: 15:04:05Z"))
		endLine()
		emptyLine()
	}

	if a.ShowMETAR {
		var metar []*METAR
		for ap := range a.Airports {
			if m := server.GetMETAR(ap); m != nil {
				metar = append(metar, m)
			}
		}

		if len(metar) > 0 {
			sort.Slice(metar, func(i, j int) bool {
				return metar[i].AirportICAO < metar[j].AirportICAO
			})

			startLine("")
			addText(basicStyle, "Weather:")
			endLine()
			for _, m := range metar {
				atis := ""
				if !a.ShowATIS {
					for _, at := range server.GetAirportATIS(m.AirportICAO) {
						atis += at.Code
					}
					if atis == "" {
						atis = " " // align
					}
				}

				startLine("")
				addText(basicStyle, "\u200a\u200a\u200a  %4s %s ", m.AirportICAO, atis)
				addText(basicStyle, "%s ", m.Altimeter)
				if m.Auto {
					addText(basicStyle, "AUTO ")
				}
				addText(basicStyle, "%s %s", m.Wind, m.Weather)
				endLine()
			}
			emptyLine()
		}
	}

	anyApproaches := false
	for _, ap := range a.activeApproaches {
		if len(ap) > 0 {
			anyApproaches = true
			break
		}
	}
	if anyApproaches {
		startLine("")
		addText(basicStyle, "Approaches:")
		endLine()

		toPrint := make(map[string][]Approach)
		maxType, maxIAFs, maxIFs := 0, 0, 0
		for _, icao := range SortedMapKeys(a.activeApproaches) {
			// Follow same order they are specified
			for _, ap := range a.approaches[icao] {
				if _, ok := a.activeApproaches[icao][ap.Code]; ok {
					toPrint[icao] = append(toPrint[icao], ap)
					maxType = max(maxType, len(ap.Type))
					maxIAFs = max(maxIAFs, len(ap.IAFs.String()))
					maxIFs = max(maxIFs, len(ap.IFs.String()))
				}
			}
		}

		for _, icao := range SortedMapKeys(toPrint) {
			// Align...
			for i, ap := range toPrint[icao] {
				startLine("")
				lineToApproach[nLines-1] = [2]string{icao, ap.Code}
				addText(basicStyle, "\u200a\u200a\u200a  %4s %3s ", Select(i == 0, icao, ""), ap.Runway)
				if _, ok := a.drawnApproaches[icao][ap.Code]; ok {
					addText(highlightStyle, "%3s ", ap.Code)
				} else {
					addText(basicStyle, "%3s ", ap.Code)
				}
				addText(basicStyle, "%-*s%-*s%-*s %s", maxType+1, ap.Type, maxIAFs+1, ap.IAFs,
					maxIFs+1, ap.IFs, ApproachFixArray{ap.FAF})
				endLine()
			}
		}

		emptyLine()
	}

	if a.ShowATIS {
		var atis []string
		for ap := range a.Airports {
			for _, at := range server.GetAirportATIS(ap) {
				airport := at.Airport
				if len(at.AppDep) > 0 {
					airport += "_" + at.AppDep
				}
				var astr string
				if len(at.Contents) > 40 {
					contents := at.Contents[:37] + "..."
					astr = fmt.Sprintf("  %-6s %s %s", airport, at.Code, contents)
				} else {
					astr = fmt.Sprintf("  %-6s %s %s", airport, at.Code, at.Contents)
				}
				atis = append(atis, astr)
			}
		}
		if len(atis) > 0 {
			sort.Strings(atis)
			startLine("")
			addText(basicStyle, "ATIS:")
			endLine()

			for _, a := range atis {
				startLine("")
				addText(basicStyle, "\u200a\u200a\u200a"+a)
				endLine()
			}
			emptyLine()
		}
	}

	var airportLocations []Point2LL
	for icao := range a.Airports {
		if ap, ok := database.airports[icao]; ok {
			airportLocations = append(airportLocations, ap.Location)
		}
	}

	var departures, airborne, landed, randomOnFreq []*Aircraft
	for _, ac := range server.GetFilteredAircraft(func(ac *Aircraft) bool {
		return ac.FlightPlan != nil && !ac.LostTrack(now)
	}) {
		if _, ok := a.Airports[ac.FlightPlan.DepartureAirport]; ok {
			if ac.OnGround() {
				departures = append(departures, ac)
			} else {
				airborne = append(airborne, ac)
			}
		} else if _, ok := a.Airports[ac.FlightPlan.ArrivalAirport]; ok {
			if ac.OnGround() {
				landed = append(landed, ac)
			}
		} else if FindIf(ac.TunedFrequencies,
			func(f Frequency) bool { return f == a.ControllerFrequency }) != -1 {
			for _, loc := range airportLocations {
				if nmdistance2ll(loc, ac.Position()) < 250 {
					randomOnFreq = append(randomOnFreq, ac)
					break
				}
			}
		}
	}

	experienceIcon := func(ac *Aircraft) {
		if positionConfig.IsFlagged(ac.Callsign) {
			if idx, ok := a.flaggedSequence[ac.Callsign]; ok && idx < 10 {
				addText(highlightStyle, "%d\u200a\u200a\u200a ", idx)
			} else {
				addText(highlightStyle, "\u200a\u200a\u200a+ ")
			}
		} else if h := ac.HoursOnNetwork(false); h < 100 && h != 0 {
			addText(basicStyle, "\u200a"+FontAwesomeIconBaby+"\u200a ")
		} else if h > 500 {
			addText(basicStyle, FontAwesomeIconGlobeAmericas+" ")
		} else {
			addText(basicStyle, " \u200a\u200a\u200a ")
		}
	}

	rules := func(ac *Aircraft) string {
		switch ac.FlightPlan.Rules {
		case IFR:
			return "I"
		case VFR:
			return "V"
		case DVFR:
			return "D"
		case SVFR:
			return "S"
		default:
			return ac.FlightPlan.Rules.String()
		}
	}

	checkSquawk := func(ac *Aircraft) {
		if ac.Mode != Charlie || ac.Squawk != ac.AssignedSquawk {
			addText(highlightStyle, " sq:")
			if ac.Mode != Charlie {
				addText(highlightStyle, "[C]")
			}
			if ac.Squawk != ac.AssignedSquawk {
				addText(highlightStyle, ac.AssignedSquawk.String())
			}
		}
	}

	controllers := server.GetAllControllers()
	frequencyToSectorId := make(map[Frequency][]string)
	for _, ctrl := range controllers {
		if pos := ctrl.GetPosition(); pos != nil {
			// TODO: handle conflicts?
			frequencyToSectorId[ctrl.Frequency] = append(frequencyToSectorId[ctrl.Frequency], pos.SectorId)
		}
	}
	for _, ids := range frequencyToSectorId {
		sort.Strings(ids)
	}

	radioTuned := func(ac *Aircraft) {
		found := false
		for _, fr := range ac.TunedFrequencies {
			if fr == 122800 {
				addText(basicStyle, "  UN")
				found = true
			} else if ids, ok := frequencyToSectorId[fr]; ok {
				for _, id := range ids {
					addText(basicStyle, "%4s", id)
				}
				found = true
			}
		}
		if !found {
			addText(basicStyle, "    ")
		}
	}

	if a.ShowRandomOnFreq && len(randomOnFreq) > 0 {
		startLine("")
		addText(basicStyle, "Randoms ["+a.ControllerFrequency.String()+"]:")
		endLine()

		sort.Slice(randomOnFreq, func(i, j int) bool {
			return randomOnFreq[i].Callsign < randomOnFreq[j].Callsign
		})
		for _, ac := range randomOnFreq {
			startLine(ac.Callsign)
			lineToCallsign[nLines-1] = ac.Callsign

			experienceIcon(ac)
			addText(basicStyle, "%-8s %s %s-%s", ac.Callsign, rules(ac),
				ac.FlightPlan.DepartureAirport, ac.FlightPlan.ArrivalAirport)
			endLine()
		}
		emptyLine()
	}

	if a.ShowDepartures && len(departures) > 0 {
		startLine("")
		addText(basicStyle, "Departures:")
		endLine()

		sort.Slice(departures, func(i, j int) bool {
			return departures[i].Callsign < departures[j].Callsign
		})
		for _, ac := range departures {
			startLine(ac.Callsign)
			lineToCallsign[nLines-1] = ac.Callsign
			route := ac.FlightPlan.Route
			if len(route) > 19 {
				route = route[:19]
				route += ".."
			}

			validAltitude := true
			dep, dok := database.airports[ac.FlightPlan.DepartureAirport]
			arr, aok := database.airports[ac.FlightPlan.ArrivalAirport]
			if dok && aok {
				east := IsEastbound(dep.Location, arr.Location)
				alt := ac.FlightPlan.Altitude

				if ac.FlightPlan.Rules == IFR {
					if alt <= 41000 {
						validAltitude = alt%1000 == 0
						if east {
							validAltitude = validAltitude && (alt/1000)%2 == 1
						} else {
							validAltitude = validAltitude && (alt/1000)%2 == 0
						}
					} else {
						if east {
							validAltitude = validAltitude && (alt == 450 || alt == 490 || alt == 530)
						} else {
							validAltitude = validAltitude && (alt == 430 || alt == 470 || alt == 510)
						}
					}
				} else {
					// VFR
					validAltitude = alt%1000 == 500 && alt < 18000
					alt -= 500
					if east {
						validAltitude = validAltitude && (alt/1000)%2 == 1
					} else {
						validAltitude = validAltitude && (alt/1000)%2 == 0
					}
				}
			}

			experienceIcon(ac)
			addText(basicStyle, "%-8s %s %s %8s ", ac.Callsign, rules(ac),
				ac.FlightPlan.DepartureAirport, ac.FlightPlan.AircraftType)
			if !validAltitude {
				addText(highlightStyle, "%6s", formatAltitude(ac.FlightPlan.Altitude))
			} else {
				addText(basicStyle, "%6s", formatAltitude(ac.FlightPlan.Altitude))
			}
			addText(basicStyle, " %-21s", route)

			radioTuned(ac)
			// Make sure the squawk is good
			checkSquawk(ac)
			endLine()
		}
		emptyLine()
	}

	// Filter out ones >100 nm from the airport
	airborne = FilterSlice(airborne, func(ac *Aircraft) bool {
		return nmdistance2ll(database.airports[ac.FlightPlan.DepartureAirport].Location, ac.Position()) < 100
	})

	if a.ShowDeparted && len(airborne) > 0 {
		sort.Slice(airborne, func(i, j int) bool {
			ai := airborne[i]
			di := nmdistance2ll(database.airports[ai.FlightPlan.DepartureAirport].Location, ai.Position())
			aj := airborne[j]
			dj := nmdistance2ll(database.airports[aj.FlightPlan.DepartureAirport].Location, aj.Position())
			return di < dj
		})

		startLine("")
		addText(basicStyle, "Departed:")
		endLine()

		for _, ac := range airborne {
			startLine(ac.Callsign)
			lineToCallsign[nLines-1] = ac.Callsign

			route := ac.FlightPlan.Route
			if len(route) > 12 {
				route = route[:12]
				route += ".."
			}

			experienceIcon(ac)
			addText(basicStyle, "%-8s %s %s %8s %6s %6s %14s", ac.Callsign, rules(ac),
				ac.FlightPlan.DepartureAirport, ac.FlightPlan.AircraftType,
				formatAltitude(ac.Altitude()), formatAltitude(ac.FlightPlan.Altitude), route)
			radioTuned(ac)
			checkSquawk(ac)
			endLine()
		}
		emptyLine()
	}

	arrivals := getDistanceSortedArrivals(a.Airports)
	if a.ShowArrivals && len(arrivals) > 0 {
		startLine("")
		addText(basicStyle, "Arrivals:")
		endLine()

		for _, arr := range arrivals {
			if arr.distance > 1000 {
				break
			}

			ac := arr.aircraft
			startLine(ac.Callsign)
			lineToCallsign[nLines-1] = ac.Callsign
			alt := ac.Altitude()
			alt = (alt + 50) / 100 * 100

			// Try to extract the STAR from the flight plan.
			route := ac.FlightPlan.Route
			star := route[strings.LastIndex(route, " ")+1:]
			if len(star) > 7 {
				star = star[len(star)-7:]
			}

			experienceIcon(ac)
			addText(basicStyle, "%-8s %s %s %8s %6s %6s %4dnm %7s", ac.Callsign, rules(ac),
				ac.FlightPlan.ArrivalAirport, ac.FlightPlan.AircraftType,
				formatAltitude(ac.TempAltitude), formatAltitude(ac.Altitude()), int(arr.distance), star)
			radioTuned(ac)
			checkSquawk(ac)
			endLine()

			if _, ok := a.seenArrivals[ac.Callsign]; !ok {
				globalConfig.AudioSettings.HandleEvent(AudioEventNewArrival)
				a.seenArrivals[ac.Callsign] = nil
			}
		}
		emptyLine()
	}

	if a.ShowLanded && len(landed) > 0 {
		startLine("")
		addText(basicStyle, "Landed:")
		endLine()

		sort.Slice(landed, func(i, j int) bool {
			return landed[i].Callsign < landed[j].Callsign
		})
		for _, ac := range landed {
			startLine(ac.Callsign)
			lineToCallsign[nLines-1] = ac.Callsign

			experienceIcon(ac)
			addText(basicStyle, "%-8s %s %8s", ac.Callsign, ac.FlightPlan.ArrivalAirport,
				ac.FlightPlan.AircraftType)
			radioTuned(ac)
			endLine()
		}
		emptyLine()
	}

	if a.ShowControllers && len(controllers) > 0 {
		startLine("")
		addText(basicStyle, "Controllers:")
		endLine()

		sort.Slice(controllers, func(i, j int) bool { return controllers[i].Callsign < controllers[j].Callsign })

		for _, suffix := range []string{"CTR", "APP", "DEP", "TWR", "GND", "DEL", "FSS", "ATIS", "OBS"} {
			first := true
			for _, ctrl := range controllers {
				if server.Callsign() == ctrl.Callsign || !strings.HasSuffix(ctrl.Callsign, suffix) {
					continue
				}

				if ctrl.Frequency == 0 {
					continue
				}

				startLine("")
				if first {
					addText(basicStyle, "  %-4s  ", suffix)
					first = false
				} else {
					addText(basicStyle, "        ")
				}

				style := Select(ctrl.RequestRelief, highlightStyle, basicStyle)
				addText(style, " %-12s %s", ctrl.Callsign, ctrl.Frequency)

				if pos := ctrl.GetPosition(); pos != nil {
					addText(basicStyle, " %-3s %s ", pos.SectorId, pos.Scope)
				} else {
					addText(basicStyle, "       ")
				}
				min := int(time.Since(ctrl.Logon).Round(time.Minute).Minutes())
				addText(basicStyle, "%02d:%02d", min/60, min%60)

				endLine()
			}
		}
	}

	// Figure out the actual sequence numbers to use for flagged aircraft next time around
	a.flaggedSequence = positionConfig.GetFlaggedSequence(drawnFlagged)

	nVisibleLines := (int(ctx.paneExtent.Height()) - a.font.size) / a.font.size
	a.sb.Update(nLines, nVisibleLines, ctx)
	textOffset := a.sb.Offset()

	td := GetTextDrawBuilder()
	defer ReturnTextDrawBuilder(td)

	sz2 := float32(a.font.size) / 2
	texty := ctx.paneExtent.Height() - sz2 + float32(textOffset*a.font.size)
	td.AddTextMulti(strs, [2]float32{sz2, texty}, styles)

	a.cb.Reset()
	ctx.SetWindowCoordinateMatrices(&a.cb)
	td.GenerateCommands(&a.cb)

	a.sb.Draw(ctx, &a.cb)

	// Handle aircraft selection
	if ctx.mouse != nil {
		y := int(texty - 1 - ctx.mouse.Pos[1])
		line := int(y) / a.font.size
		if appr, ok := lineToApproach[line]; ok {
			if ctx.mouse.Clicked[MouseButtonPrimary] {
				if _, ok := a.drawnApproaches[appr[0]][appr[1]]; ok {
					delete(a.drawnApproaches[appr[0]], appr[1])
				} else {
					a.drawnApproaches[appr[0]][appr[1]] = nil
				}
			}
		} else if callsign, ok := lineToCallsign[line]; ok {
			if ctx.mouse.Clicked[MouseButtonPrimary] {
				positionConfig.selectedAircraft = server.GetAircraft(callsign)
			}
			if ctx.mouse.Clicked[MouseButtonSecondary] {
				positionConfig.ToggleFlagged(callsign)
			}
		}
	}

	cb.Call(a.cb)
}

func (a *AirportInfoPane) DrawScope(ctx *PaneContext, transforms ScopeTransformations, cb *CommandBuffer, font *Font) {
	td := GetTextDrawBuilder()
	defer ReturnTextDrawBuilder(td)
	ld := GetLinesDrawBuilder()
	defer ReturnLinesDrawBuilder(ld)

	cs := positionConfig.GetColorScheme()
	for _, icao := range SortedMapKeys(a.drawnApproaches) {
		aploc, _ := database.Locate(icao)

		// Follow same order they are specified
		for _, ap := range a.approaches[icao] {
			if _, ok := a.drawnApproaches[icao][ap.Code]; ok {
				if _, ok := a.activeApproaches[icao][ap.Code]; !ok {
					// don't draw it if it was removed from the active set
					continue
				}

				faf, _ := database.Locate(ap.FAF.Fix)
				iafs := MapSlice(ap.IAFs, func(a ApproachFix) Point2LL {
					p, _ := database.Locate(a.Fix)
					return p
				})
				ifs := MapSlice(ap.IFs, func(a ApproachFix) Point2LL {
					p, _ := database.Locate(a.Fix)
					return p
				})

				ld.AddLine(aploc, faf)
				for _, p := range ifs {
					ld.AddLine(faf, p)
					for _, pp := range iafs {
						ld.AddLine(p, pp)
					}
				}

				addText := func(a ApproachFix) {
					p, _ := database.Locate(a.Fix)
					pw := transforms.WindowFromLatLongP(p)
					pw = add2f(pw, a.DrawOffset)
					if a.DrawOffset[0] < 0 {
						// align with the right side of the text
						w, _ := font.BoundText(a.String(), 0)
						pw[0] -= float32(w)
					}
					td.AddText(a.String(), pw, TextStyle{Font: font, Color: cs.Fix})
				}
				addText(ap.FAF)
				for _, a := range ap.IAFs {
					addText(a)
				}
				for _, a := range ap.IFs {
					addText(a)
				}

			}
		}
	}

	cb.SetRGB(cs.Fix)
	transforms.LoadLatLongViewingMatrices(cb)
	ld.GenerateCommands(cb)
	transforms.LoadWindowViewingMatrices(cb)
	td.GenerateCommands(cb)

}

///////////////////////////////////////////////////////////////////////////
// EmptyPane

type EmptyPane struct {
	// Empty struct types may all have the same address, which breaks
	// assorted assumptions elsewhere in the system....
	wtfgo int
}

func NewEmptyPane() *EmptyPane { return &EmptyPane{} }

func (ep *EmptyPane) Activate()                  {}
func (ep *EmptyPane) Deactivate()                {}
func (ep *EmptyPane) CanTakeKeyboardFocus() bool { return false }

func (ep *EmptyPane) Duplicate(nameAsCopy bool) Pane { return &EmptyPane{} }
func (ep *EmptyPane) Name() string                   { return "(Empty)" }

func (ep *EmptyPane) Draw(ctx *PaneContext, cb *CommandBuffer) {}

///////////////////////////////////////////////////////////////////////////
// FlightPlanPane

type FlightPlanPane struct {
	FontIdentifier FontIdentifier
	font           *Font

	ShowRemarks bool
}

func NewFlightPlanPane() *FlightPlanPane {
	return &FlightPlanPane{}
}

func (fp *FlightPlanPane) Activate() {
	if fp.font = GetFont(fp.FontIdentifier); fp.font == nil {
		fp.font = GetDefaultFont()
		fp.FontIdentifier = fp.font.id
	}
}

func (fp *FlightPlanPane) Deactivate()                {}
func (fp *FlightPlanPane) CanTakeKeyboardFocus() bool { return false }

func (fp *FlightPlanPane) DrawUI() {
	imgui.Checkbox("Show remarks", &fp.ShowRemarks)
	if newFont, changed := DrawFontPicker(&fp.FontIdentifier, "Font"); changed {
		fp.font = newFont
	}
}

func (fp *FlightPlanPane) Duplicate(nameAsCopy bool) Pane {
	return &FlightPlanPane{FontIdentifier: fp.FontIdentifier, font: fp.font}
}

func (fp *FlightPlanPane) Name() string { return "Flight Plan" }

func (fp *FlightPlanPane) Draw(ctx *PaneContext, cb *CommandBuffer) {
	ac := positionConfig.selectedAircraft
	if ac == nil {
		return
	}

	td := GetTextDrawBuilder()
	defer ReturnTextDrawBuilder(td)

	contents, _ := ac.GetFormattedFlightPlan(fp.ShowRemarks)

	sz2 := float32(fp.font.size) / 2
	spaceWidth, _ := fp.font.BoundText(" ", 0)
	ncols := (int(ctx.paneExtent.Width()) - fp.font.size) / spaceWidth
	indent := 3 + len(ac.Callsign)
	if ac.VoiceCapability != VoiceFull {
		indent += 2
	}
	wrapped, _ := wrapText(contents, ncols, indent, true)
	td.AddText(wrapped, [2]float32{sz2, ctx.paneExtent.Height() - sz2},
		TextStyle{Font: fp.font, Color: ctx.cs.Text})

	ctx.SetWindowCoordinateMatrices(cb)
	td.GenerateCommands(cb)
}

///////////////////////////////////////////////////////////////////////////
// ImageViewPane

type ImageViewPane struct {
	Directory         string
	SelectedImage     string
	ImageCalibrations map[string]*ImageCalibration
	InvertImages      bool

	DrawAircraft bool
	AircraftSize int32

	scale         float32
	offset        [2]float32
	mouseDragging bool

	enteredFixPos    [2]float32
	enteredFix       string
	enteredFixCursor int

	nImagesLoading int
	ctx            context.Context
	cancel         context.CancelFunc
	loadChan       chan LoadedImage

	expanded        map[string]interface{}
	loadedImages    map[string]*ImageViewImage
	showImageList   bool
	scrollBar       *ScrollBar
	dirSelectDialog *FileSelectDialogBox
}

type ImageCalibration struct {
	Fix     [2]string
	Pimage  [2][2]float32
	lastSet int
}

type LoadedImage struct {
	Name    string
	Pyramid []image.Image
}

type ImageViewImage struct {
	Name        string
	TexId       uint32
	AspectRatio float32
}

func NewImageViewPane() *ImageViewPane {
	return &ImageViewPane{
		Directory:         "/Users/mmp/vatsim/KPHL",
		ImageCalibrations: make(map[string]*ImageCalibration),
		AircraftSize:      16,
	}
}

func (iv *ImageViewPane) Duplicate(nameAsCopy bool) Pane {
	dupe := &ImageViewPane{
		Directory:         iv.Directory,
		SelectedImage:     iv.SelectedImage,
		ImageCalibrations: DuplicateMap(iv.ImageCalibrations),
		InvertImages:      iv.InvertImages,
		DrawAircraft:      iv.DrawAircraft,
		AircraftSize:      iv.AircraftSize,
		scrollBar:         NewScrollBar(4, false),
	}
	dupe.loadImages()
	return dupe
}

func (iv *ImageViewPane) Activate() {
	iv.scrollBar = NewScrollBar(4, false)
	iv.expanded = make(map[string]interface{})
	iv.scale = 1

	iv.loadImages()
}

func (iv *ImageViewPane) loadImages() {
	iv.loadedImages = make(map[string]*ImageViewImage)
	iv.ctx, iv.cancel = context.WithCancel(context.Background())
	iv.loadChan = make(chan LoadedImage, 64)
	iv.nImagesLoading = 0

	// Load the selected image first, for responsiveness...
	iv.nImagesLoading++
	loadImage(iv.ctx, path.Join(iv.Directory, iv.SelectedImage), iv.InvertImages, iv.loadChan)

	// Now kick off loading the rest asynchronously
	err := filepath.WalkDir(iv.Directory, func(filename string, entry os.DirEntry, err error) error {
		// TODO: figure out how to handle this... It's likely a permissions issue or the like.
		if err != nil {
			return err
		}

		ext := filepath.Ext(strings.ToUpper(entry.Name()))
		if entry.IsDir() || (ext != ".PNG" && ext != ".JPG" && ext != ".JPEG") {
			return nil
		}
		if entry.Name() == iv.SelectedImage {
			// Already loaded it
			return nil
		}

		iv.nImagesLoading++
		go loadImage(iv.ctx, filename, iv.InvertImages, iv.loadChan)
		return nil
	})
	if err != nil {
		lg.Errorf("%s: %v", iv.Directory, err)
	}
}

func loadImage(ctx context.Context, path string, invertImage bool, loadChan chan LoadedImage) {
	f, err := os.Open(path)
	if err != nil {
		lg.Errorf("%s: %v", path, err)
		return
	}
	defer f.Close()

	var img image.Image
	switch filepath.Ext(strings.ToUpper(path)) {
	case ".PNG":
		img, err = png.Decode(f)

	case ".JPG", ".JPEG":
		img, err = jpeg.Decode(f)
	}

	if err != nil {
		lg.Errorf("%s: unable to decode image: %v", path, err)
		return
	}

	if img != nil {
		if invertImage {
			rgbaImage, ok := img.(*image.RGBA)
			if !ok {
				rgbaImage = image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
				draw.Draw(rgbaImage, rgbaImage.Bounds(), img, img.Bounds().Min, draw.Src)
			}

			b := rgbaImage.Bounds()
			for py := b.Min.Y; py < b.Max.Y; py++ {
				for px := b.Min.X; px < b.Max.X; px++ {
					offset := 4*px + rgbaImage.Stride*py
					rgba := color.RGBA{
						R: rgbaImage.Pix[offset],
						G: rgbaImage.Pix[offset+1],
						B: rgbaImage.Pix[offset+2],
						A: rgbaImage.Pix[offset+3]}

					r, g, b := float32(rgba.R)/255, float32(rgba.G)/255, float32(rgba.B)/255
					// convert to YIQ
					y, i, q := .299*r+.587*g+.114*b, .596*r-.274*g-.321*b, .211*r-.523*g+.311*b
					// invert luminance
					y = 1 - y
					// And back...
					r, g, b = y+.956*i+.621*q, y-.272*i-.647*q, y-1.107*i+1.705*q
					quant := func(f float32) uint8 {
						f *= 255
						if f < 0 {
							f = 0
						} else if f > 255 {
							f = 255
						}
						return uint8(f)
					}

					rgbaImage.Pix[offset] = quant(r)
					rgbaImage.Pix[offset+1] = quant(g)
					rgbaImage.Pix[offset+2] = quant(b)
				}
			}
			img = rgbaImage
		}

		pyramid := GenerateImagePyramid(img)
		for {
			select {
			case <-ctx.Done():
				// Canceled; exit
				return

			case loadChan <- LoadedImage{Name: path, Pyramid: pyramid}:
				// success
				return

			case <-time.After(50 * time.Millisecond):
				// sleep
			}
		}
	}
}

func (iv *ImageViewPane) Deactivate() {
	iv.clearImages()
}

func (iv *ImageViewPane) clearImages() {
	iv.cancel()
	iv.loadChan = nil

	for _, im := range iv.loadedImages {
		renderer.DestroyTexture(im.TexId)
	}
	iv.loadedImages = nil
	iv.nImagesLoading = 0
}

func (iv *ImageViewPane) CanTakeKeyboardFocus() bool { return true }

func (iv *ImageViewPane) Name() string { return path.Base(iv.Directory) }

func (iv *ImageViewPane) DrawUI() {
	imgui.Checkbox("Draw aircraft", &iv.DrawAircraft)
	if iv.DrawAircraft {
		imgui.SliderIntV("Aircraft size", &iv.AircraftSize, 4, 64, "%d", 0)
	}

	imgui.Text("Directory: " + iv.Directory)
	imgui.SameLine()
	if imgui.Button("Select...") {
		if iv.dirSelectDialog == nil {
			iv.dirSelectDialog = NewDirectorySelectDialogBox("Select image directory...",
				iv.Directory, func(d string) {
					if d == iv.Directory {
						return
					}
					iv.clearImages()
					iv.Directory = d
					iv.SelectedImage = ""
					iv.loadImages()
				})
		}
		iv.dirSelectDialog.Activate()
	}
	if iv.dirSelectDialog != nil {
		iv.dirSelectDialog.Draw()
	}

	if imgui.Checkbox("Invert images", &iv.InvertImages) {
		iv.clearImages()
		iv.loadImages()
	}

	// TODO?: refresh button
}

func (iv *ImageViewPane) Draw(ctx *PaneContext, cb *CommandBuffer) {
	if iv.nImagesLoading > 0 {
		select {
		case im := <-iv.loadChan:
			texid := renderer.CreateTextureFromImages(im.Pyramid)
			aspect := float32(im.Pyramid[0].Bounds().Max.X-im.Pyramid[0].Bounds().Min.X) /
				float32(im.Pyramid[0].Bounds().Max.Y-im.Pyramid[0].Bounds().Min.Y)

			// strip the image directory name
			name, err := filepath.Rel(iv.Directory, im.Name)
			if err != nil {
				lg.Errorf("%s: %v", im.Name, err)
			}

			iv.loadedImages[name] = &ImageViewImage{
				Name:        name,
				TexId:       texid,
				AspectRatio: aspect,
			}

			iv.nImagesLoading--

		default:
		}
	}

	ctx.SetWindowCoordinateMatrices(cb)

	enteringCalibration := wm.keyboardFocusPane == iv

	if !enteringCalibration && iv.showImageList {
		iv.drawImageList(ctx, cb)
	} else {
		quad := iv.drawImage(ctx, cb)
		iv.drawAircraft(ctx, cb)
		iv.handleCalibration(ctx, cb)

		if ctx.mouse != nil {
			if ctx.mouse.Wheel[1] != 0 {
				oldScale := iv.scale
				iv.scale *= pow(1.125, -ctx.mouse.Wheel[1])

				if image, ok := iv.loadedImages[iv.SelectedImage]; ok {
					e := iv.getImageExtent(image, ctx)

					// Limit zoom out so that it always fills the pane either vertically or horizontally
					w, h := ctx.paneExtent.Width(), ctx.paneExtent.Height()
					xInset := e.p0[0] > 0 && e.p1[0] < w-1
					yInset := e.p0[1] > 0 && e.p1[1] < h-1
					if xInset && yInset {
						iv.scale = oldScale
					}
				}
			}

			inQuad := quad.Inside(ctx.mouse.Pos)

			if ctx.mouse.Dragging[MouseButtonPrimary] && inQuad {
				iv.mouseDragging = true
			} else if ctx.mouse.Released[0] && !iv.mouseDragging && !inQuad {
				iv.showImageList = true
			}

			if iv.mouseDragging {
				iv.offset = add2f(iv.offset, ctx.mouse.DragDelta)
				if ctx.mouse.Released[0] {
					iv.mouseDragging = false
				}
			}
		}
	}

	if ctx.keyboard != nil && ctx.mouse != nil { // check mouse since otherwise multiple panes pick up the message
		if _, ok := ctx.keyboard.Pressed[KeyEscape]; ok {
			iv.showImageList = !iv.showImageList
		}
	}
}

func (iv *ImageViewPane) drawImageList(ctx *PaneContext, cb *CommandBuffer) {
	font := ui.fixedFont
	indent := float32(int(font.size / 2)) // left and top spacing
	lineHeight := font.size

	nVisibleLines := (int(ctx.paneExtent.Height()) - font.size) / font.size
	iv.scrollBar.Update(len(iv.loadedImages), nVisibleLines, ctx)
	iv.scrollBar.Draw(ctx, cb)
	textOffset := iv.scrollBar.Offset()

	// Current cursor position
	pText := [2]float32{indent, ctx.paneExtent.Height() - indent + float32(textOffset*font.size)}

	td := GetTextDrawBuilder()
	defer ReturnTextDrawBuilder(td)
	style := TextStyle{Font: font, Color: ctx.cs.Text}
	selectedStyle := TextStyle{Font: font, Color: ctx.cs.TextHighlight}

	shownDirs := make(map[string]interface{})

	selected := func() bool {
		// hovered?
		if !(ctx.mouse != nil && ctx.mouse.Pos[1] < pText[1] && ctx.mouse.Pos[1] >= pText[1]-float32(lineHeight)) {
			return false
		}

		// Draw the selection box.
		rect := LinesDrawBuilder{}
		width := ctx.paneExtent.Width()
		rect.AddPolyline([2]float32{indent / 2, float32(pText[1])},
			[][2]float32{[2]float32{0, 0},
				[2]float32{width - indent, 0},
				[2]float32{width - indent, float32(-lineHeight)},
				[2]float32{0, float32(-lineHeight)}})
		cb.SetRGB(ctx.cs.Text)
		rect.GenerateCommands(cb)

		return ctx.mouse.Released[0]
	}

	for _, name := range SortedMapKeys(iv.loadedImages) {
		var dirs []string
		if runtime.GOOS == "windows" {
			dirs = strings.Split(filepath.Dir(name), "\\")
		} else {
			dirs = strings.Split(filepath.Dir(name), "/")
		}
		dir := ""

		offerImage := true
		atRoot := len(dirs) == 1 && dirs[0] == "."
		if !atRoot {
			for i, d := range dirs {
				dir = path.Join(dir, d)
				_, expanded := iv.expanded[dir]
				_, shown := shownDirs[dir]

				offerImage = offerImage && expanded

				if shown {
					if expanded {
						continue
					} else {
						break
					}
				}

				shownDirs[dir] = nil

				var text string
				if expanded {
					text = FontAwesomeIconCaretDown + "\u200a" + d
				} else {
					text = "\u200a\u200a" + FontAwesomeIconCaretRight + "\u200a\u200a" + d
				}

				if selected() {
					if expanded {
						delete(iv.expanded, dir)
					} else {
						iv.expanded[dir] = nil
					}
				}

				indent := fmt.Sprintf("%*c", 2*(i+1)-1, ' ')
				pText = td.AddText(indent+text+"\n", pText, style)

				if !expanded {
					// Don't show the children
					break
				}
			}
		}
		if offerImage {
			if selected() {
				iv.SelectedImage = name
				iv.showImageList = false
				iv.scale = 1
				iv.offset = [2]float32{0, 0}
			}
			indent := fmt.Sprintf("%*c", 2*len(dirs)+1, ' ')
			if atRoot {
				indent = " "
			}
			if name == iv.SelectedImage {
				pText = td.AddText(indent+filepath.Base(name)+"\n", pText, selectedStyle)
			} else {
				pText = td.AddText(indent+filepath.Base(name)+"\n", pText, style)
			}
		}
	}

	td.GenerateCommands(cb)
}

func (iv *ImageViewPane) drawImage(ctx *PaneContext, cb *CommandBuffer) Extent2D {
	image, ok := iv.loadedImages[iv.SelectedImage]
	if !ok {
		return Extent2D{}
	}

	// Draw the selected image
	td := GetTexturedTrianglesDrawBuilder()
	defer ReturnTexturedTrianglesDrawBuilder(td)

	e := iv.getImageExtent(image, ctx)
	p := [4][2]float32{e.p0, [2]float32{e.p1[0], e.p0[1]}, e.p1, [2]float32{e.p0[0], e.p1[1]}}

	td.AddQuad(p[0], p[1], p[2], p[3], [2]float32{0, 1}, [2]float32{1, 1}, [2]float32{1, 0}, [2]float32{0, 0})

	cb.SetRGB(RGB{1, 1, 1})
	td.GenerateCommands(image.TexId, cb)

	// Possibly start calibration specification
	if ctx.mouse != nil && ctx.mouse.Released[1] {
		// remap mouse position from window coordinates to normalized [0,1]^2 image coordinates
		iv.enteredFixPos = sub2f(ctx.mouse.Pos, e.p0)
		iv.enteredFixPos[0] /= e.Width()
		iv.enteredFixPos[1] /= e.Height()
		iv.enteredFix = ""
		iv.enteredFixCursor = 0
		wmTakeKeyboardFocus(iv, true)
	}

	return Extent2DFromPoints([][2]float32{p[0], p[2]})
}

// returns window-space extent
func (iv *ImageViewPane) getImageExtent(image *ImageViewImage, ctx *PaneContext) Extent2D {
	w, h := ctx.paneExtent.Width(), ctx.paneExtent.Height()
	var dw, dh float32
	if image.AspectRatio > w/h {
		h = w / image.AspectRatio
		dh = (ctx.paneExtent.Height() - h) / 2
	} else {
		w = h * image.AspectRatio
		dw = (ctx.paneExtent.Width() - w) / 2
	}

	p0 := [2]float32{dw, dh}
	e := Extent2D{p0: p0, p1: add2f(p0, [2]float32{w, h})}

	// Scale and offset
	e.p0 = add2f(e.p0, iv.offset)
	e.p1 = add2f(e.p1, iv.offset)
	center := mid2f(e.p0, e.p1)
	e.p0 = add2f(scale2f(sub2f(e.p0, center), iv.scale), center)
	e.p1 = add2f(scale2f(sub2f(e.p1, center), iv.scale), center)

	return e
}

func (iv *ImageViewPane) drawAircraft(ctx *PaneContext, cb *CommandBuffer) {
	if !iv.DrawAircraft {
		return
	}

	cal, ok := iv.ImageCalibrations[iv.SelectedImage]
	if !ok {
		return
	}

	var pll [2]Point2LL
	if pll[0], ok = database.Locate(cal.Fix[0]); !ok {
		return
	}
	if pll[1], ok = database.Locate(cal.Fix[1]); !ok {
		return
	}

	var image *ImageViewImage
	if image, ok = iv.loadedImages[iv.SelectedImage]; !ok {
		return
	}

	// Find the  window coordinates of the marked points
	var pw [2][2]float32
	e := iv.getImageExtent(image, ctx)
	for i := 0; i < 2; i++ {
		pw[i] = e.Lerp(cal.Pimage[i])
	}

	// rotate to align
	llTheta := atan2(pll[1][1]-pll[0][1], pll[1][0]-pll[0][0])
	wTheta := atan2(pw[1][1]-pw[0][1], pw[1][0]-pw[0][0])
	scale := distance2f(pw[0], pw[1]) / distance2f(pll[0], pll[1])

	windowFromLatLong := Identity3x3().
		// translate so that the origin is at pw[0]
		Translate(pw[0][0], pw[0][1]).
		// scale it so that the second points line up
		Scale(scale, scale).
		// rotate to align the vector from p0 to p1 in texture space
		// with the vector from p0 to p1 in window space
		Rotate(wTheta-llTheta).
		// translate so pll[0] is the origin
		Translate(-pll[0][0], -pll[0][1])

	var icons []PlaneIconSpec
	// FIXME: draw in consistent order
	for _, ac := range server.GetAllAircraft() {
		// FIXME: cull based on altitude range
		icons = append(icons, PlaneIconSpec{
			P:       windowFromLatLong.TransformPoint(ac.Position()),
			Heading: ac.Heading(),
			Size:    float32(iv.AircraftSize)})
	}
	DrawPlaneIcons(icons, ctx.cs.Track, cb)
}

func (iv *ImageViewPane) handleCalibration(ctx *PaneContext, cb *CommandBuffer) {
	enteringCalibration := wm.keyboardFocusPane == iv

	if !enteringCalibration {
		return
	}

	if ctx.keyboard != nil && ctx.keyboard.IsPressed(KeyEscape) {
		wmReleaseKeyboardFocus()
		return
	}

	// indicate if it's invalid
	pInput := [2]float32{10, 20}
	td := GetTextDrawBuilder()
	defer ReturnTextDrawBuilder(td)
	if _, ok := database.Locate(iv.enteredFix); iv.enteredFix != "" && !ok {
		style := TextStyle{Font: ui.fixedFont, Color: ctx.cs.Error}
		pInput = td.AddText(FontAwesomeIconExclamationTriangle+" ", pInput, style)
	}

	inputStyle := TextStyle{Font: ui.fixedFont, Color: ctx.cs.Text}
	pInput = td.AddText("> ", pInput, inputStyle)
	td.GenerateCommands(cb)

	cursorStyle := TextStyle{Font: ui.fixedFont, Color: ctx.cs.Background,
		DrawBackground: true, BackgroundColor: ctx.cs.Text}
	exit, _ := uiDrawTextEdit(&iv.enteredFix, &iv.enteredFixCursor, ctx.keyboard, pInput,
		inputStyle, cursorStyle, cb)
	iv.enteredFix = strings.ToUpper(iv.enteredFix)

	if exit == TextEditReturnEnter {
		cal, ok := iv.ImageCalibrations[iv.SelectedImage]
		if !ok {
			cal = &ImageCalibration{}
			iv.ImageCalibrations[iv.SelectedImage] = cal
		}

		for i, fix := range cal.Fix {
			if fix == iv.enteredFix {
				// new location for existing one
				cal.Pimage[i] = iv.enteredFixPos
				return
			}
		}
		// find a slot. any unset?
		for i, fix := range cal.Fix {
			if fix == "" {
				cal.Pimage[i] = iv.enteredFixPos
				cal.Fix[i] = iv.enteredFix
				return
			}
		}

		// alternate between the two
		i := (cal.lastSet + 1) % len(cal.Fix)
		cal.Pimage[i] = iv.enteredFixPos
		cal.Fix[i] = iv.enteredFix
		cal.lastSet = i
	}
}

///////////////////////////////////////////////////////////////////////////
// TabbedPane

type TabbedPane struct {
	ThumbnailHeight int32
	ActivePane      int
	Panes           []Pane `json:"-"` // These are serialized manually below...

	dragPane Pane
}

func NewTabbedPane() *TabbedPane {
	return &TabbedPane{
		ThumbnailHeight: 128,
	}
}

func (tp *TabbedPane) MarshalJSON() ([]byte, error) {
	// We actually do want the Panes marshaled automatically, though we
	// need to carry along their types as well.
	type JSONTabbedPane struct {
		TabbedPane
		PanesCopy []Pane `json:"Panes"`
		PaneTypes []string
	}

	jtp := JSONTabbedPane{TabbedPane: *tp}
	jtp.PanesCopy = tp.Panes
	for _, p := range tp.Panes {
		jtp.PaneTypes = append(jtp.PaneTypes, fmt.Sprintf("%T", p))
	}

	return json.Marshal(jtp)
}

func (tp *TabbedPane) UnmarshalJSON(data []byte) error {
	// First unmarshal all of the stuff that can be done automatically; using
	// a separate type avoids an infinite loop of UnmarshalJSON calls.
	type LocalTabbedPane TabbedPane
	var ltp LocalTabbedPane
	if err := json.Unmarshal(data, &ltp); err != nil {
		return err
	}
	*tp = (TabbedPane)(ltp)

	// Now do the panes.
	var p struct {
		PaneTypes []string
		Panes     []any
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}

	for i, paneType := range p.PaneTypes {
		// It's a little messy to round trip from unmarshaling into []any
		// and then re-marshaling each one individually into bytes, but I
		// haven't found a cleaner way.
		data, err := json.Marshal(p.Panes[i])
		if err != nil {
			return err
		}
		if pane, err := unmarshalPane(paneType, data); err != nil {
			return err
		} else {
			tp.Panes = append(tp.Panes, pane)
		}
	}

	return nil
}

func (tp *TabbedPane) Duplicate(nameAsCopy bool) Pane {
	dupe := &TabbedPane{}
	for _, p := range tp.Panes {
		dupe.Panes = append(dupe.Panes, p.Duplicate(nameAsCopy))
	}
	return dupe
}

func (tp *TabbedPane) Activate() {
	for _, p := range tp.Panes {
		p.Activate()
	}
}

func (tp *TabbedPane) Deactivate() {
	for _, p := range tp.Panes {
		p.Deactivate()
	}
}

func (tp *TabbedPane) CanTakeKeyboardFocus() bool {
	for _, p := range tp.Panes {
		if p.CanTakeKeyboardFocus() {
			return true
		}
	}
	return false
}

func (tp *TabbedPane) Name() string {
	return "Tabbed window"
}

func (tp *TabbedPane) DrawUI() {
	imgui.SliderIntV("Thumbnail height", &tp.ThumbnailHeight, 8, 256, "%d", 0)
	name, pane := uiDrawNewPaneSelector("Add new window...", "")
	if name != "" {
		pane.Activate()
		tp.Panes = append(tp.Panes, pane)
	}

	flags := imgui.TableFlagsBordersH | imgui.TableFlagsBordersOuterV | imgui.TableFlagsRowBg
	if imgui.BeginTableV("##panes", 4, flags, imgui.Vec2{}, 0.0) {
		imgui.TableSetupColumn("Windows")
		imgui.TableSetupColumn("Up")
		imgui.TableSetupColumn("Down")
		imgui.TableSetupColumn("Trash")
		for i, pane := range tp.Panes {
			imgui.TableNextRow()
			imgui.TableNextColumn()

			imgui.PushID(fmt.Sprintf("%d", i))
			if imgui.SelectableV(pane.Name(), i == tp.ActivePane, 0, imgui.Vec2{}) {
				tp.ActivePane = i
			}

			imgui.TableNextColumn()
			if i > 0 && len(tp.Panes) > 1 {
				if imgui.Button(FontAwesomeIconArrowUp) {
					tp.Panes[i], tp.Panes[i-1] = tp.Panes[i-1], tp.Panes[i]
					if tp.ActivePane == i {
						tp.ActivePane = i - 1
					}
				}
			}
			imgui.TableNextColumn()
			if i+1 < len(tp.Panes) {
				if imgui.Button(FontAwesomeIconArrowDown) {
					tp.Panes[i], tp.Panes[i+1] = tp.Panes[i+1], tp.Panes[i]
					if tp.ActivePane == i {
						tp.ActivePane = i + 1
					}
				}
			}
			imgui.TableNextColumn()
			if imgui.Button(FontAwesomeIconTrash) {
				tp.Panes = FilterSlice(tp.Panes, func(p Pane) bool { return p != pane })
			}
			imgui.PopID()
		}
		imgui.EndTable()
	}

	if tp.ActivePane < len(tp.Panes) {
		if uid, ok := tp.Panes[tp.ActivePane].(PaneUIDrawer); ok {
			imgui.Separator()
			imgui.Text(tp.Panes[tp.ActivePane].Name() + " configuration")
			uid.DrawUI()
		}
	}
}

func (tp *TabbedPane) Draw(ctx *PaneContext, cb *CommandBuffer) {
	// final aspect ratio, after the thumbnails at the top:
	// TODO (adjust to fit)
	w, h := ctx.paneExtent.Width(), ctx.paneExtent.Height()
	thumbHeight := float32(tp.ThumbnailHeight)
	aspect := w / (h - thumbHeight)
	thumbWidth := float32(int(aspect * thumbHeight))
	highDPIScale := platform.FramebufferSize()[1] / platform.DisplaySize()[1]

	nThumbs := len(tp.Panes)
	if thumbWidth*float32(nThumbs) > w {
		thumbWidth = floor(w / float32(nThumbs))
		// aspect = w / (h - thumbHeight)
		// aspect = w / (h - (thumbWidth / aspect)) ->
		aspect = (w + thumbWidth) / h
		thumbHeight = floor(thumbWidth / aspect)
	}

	// Center the thumbnails horizontally
	x0 := (w - thumbWidth*float32(nThumbs)) / 2
	x1 := x0 + thumbWidth*float32(nThumbs)

	// Draw the labels above the panes
	labelHeight := 2 + ui.fixedFont.size
	td := GetTextDrawBuilder()
	defer ReturnTextDrawBuilder(td)
	style := TextStyle{Font: ui.fixedFont, Color: ctx.cs.Text}
	for i, pane := range tp.Panes {
		label := pane.Name()
		// Find first shortened-if-need-be label that fits the space
		for len(label) > 0 {
			textw, _ := ui.fixedFont.BoundText(label, 0)
			if float32(textw) < thumbWidth {
				pText := [2]float32{x0 + (float32(i)+0.5)*thumbWidth - float32(textw/2), h - 1}
				td.AddText(label, pText, style)
				break
			}
			label = label[:len(label)-1]
		}
	}

	ctx.SetWindowCoordinateMatrices(cb)
	td.GenerateCommands(cb)

	h -= float32(labelHeight)

	// Draw the thumbnail panes
	ld := GetColoredLinesDrawBuilder()
	defer ReturnColoredLinesDrawBuilder(ld)

	// Draw top/bottom lines
	ld.AddLine([2]float32{x0, h}, [2]float32{x1, h}, ctx.cs.UIControl)
	ld.AddLine([2]float32{x0, h - thumbHeight}, [2]float32{x1, h - thumbHeight}, ctx.cs.UIControl)

	// Vertical separator lines
	for i := 0; i <= nThumbs; i++ {
		x := x0 + float32(i)*thumbWidth
		if i == tp.ActivePane || i-1 == tp.ActivePane {
			ld.AddLine([2]float32{x, h}, [2]float32{x, h - thumbHeight}, ctx.cs.UIControlActive)
			if i == tp.ActivePane {
				ld.AddLine([2]float32{x, h}, [2]float32{x + thumbWidth, h}, ctx.cs.UIControlActive)
				ld.AddLine([2]float32{x, h - thumbHeight}, [2]float32{x + thumbWidth, h - thumbHeight}, ctx.cs.UIControlActive)
			}
		} else {
			ld.AddLine([2]float32{x, h}, [2]float32{x, h - thumbHeight}, ctx.cs.UIControl)
		}
	}

	cb.LineWidth(1)
	ld.GenerateCommands(cb)

	// Draw the thumbnail contents
	px0 := x0
	for _, pane := range tp.Panes {
		paneCtx := *ctx
		paneCtx.mouse = nil
		paneCtx.thumbnail = true

		// 1px offsets to preserve separator lines
		paneCtx.paneExtent = Extent2D{
			p0: [2]float32{ctx.paneExtent.p0[0] + px0 + 1, ctx.paneExtent.p0[1] + h - thumbHeight + 1},
			p1: [2]float32{ctx.paneExtent.p0[0] + px0 + thumbWidth - 1, ctx.paneExtent.p0[1] + h - 1},
		}
		sv := paneCtx.paneExtent.Scale(highDPIScale)

		cb.Scissor(int(sv.p0[0]), int(sv.p0[1]), int(sv.Width()), int(sv.Height()))
		cb.Viewport(int(sv.p0[0]), int(sv.p0[1]), int(sv.Width()), int(sv.Height()))

		// FIXME: what if pane calls wmTakeKeyboard focus. Will things get
		// confused?
		pane.Draw(&paneCtx, cb)
		cb.ResetState()

		px0 += thumbWidth
	}

	// Draw the active pane
	if tp.ActivePane < len(tp.Panes) {
		// Scissor to limit to the region below the thumbnails
		paneCtx := *ctx
		paneCtx.paneExtent.p1[1] = paneCtx.paneExtent.p0[1] + h - thumbHeight
		sv := paneCtx.paneExtent.Scale(highDPIScale)
		cb.Scissor(int(sv.p0[0]), int(sv.p0[1]), int(sv.Width()), int(sv.Height()))
		cb.Viewport(int(sv.p0[0]), int(sv.p0[1]), int(sv.Width()), int(sv.Height()))

		// Don't pass along mouse events if the mouse is outside of the
		// draw region (e.g., a thumbnail is being clicked...)
		if paneCtx.mouse != nil && paneCtx.mouse.Pos[1] >= h-thumbHeight {
			paneCtx.mouse = nil
		}

		tp.Panes[tp.ActivePane].Draw(&paneCtx, cb)

		cb.ResetState()
	}

	// See if a thumbnail was clicked on
	if ctx.mouse != nil && ctx.mouse.Released[MouseButtonPrimary] && ctx.mouse.Pos[1] >= h-thumbHeight {
		i := int((ctx.mouse.Pos[0] - x0) / thumbWidth)
		if i >= 0 && i < len(tp.Panes) {
			tp.ActivePane = i
		}
	}
}
