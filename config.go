// config.go
// Copyright(c) 2022 Matt Pharr, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/mmp/imgui-go/v4"
)

// Things that apply to all configs
type GlobalConfig struct {
	NotesFile   string
	AliasesFile string

	PositionConfigs       map[string]*PositionConfig
	ActivePosition        string
	ColorSchemes          map[string]*ColorScheme
	InitialWindowSize     [2]int
	InitialWindowPosition [2]int
	ImGuiSettings         string
	AudioSettings         AudioSettings

	aliases map[string]string

	notesRoot *NotesNode
}

type NotesNode struct {
	title    string
	text     []string
	children []*NotesNode
}

type PositionConfig struct {
	SectorFile      string
	PositionFile    string
	ColorSchemeName string
	DisplayRoot     *DisplayNode

	todos  []ToDoReminderItem
	timers []TimerReminderItem

	selectedAircraft *Aircraft

	highlightedLocation        Point2LL
	highlightedLocationEndTime time.Time
	drawnRoute                 string
	drawnRouteEndTime          time.Time
	sessionDrawVORs            map[string]interface{}
	sessionDrawNDBs            map[string]interface{}
	sessionDrawFixes           map[string]interface{}
	sessionDrawAirports        map[string]interface{}

	eventsId EventSubscriberId
}

// Some UI state that needs  to stick around
var (
	serverComboState *ComboBoxState = NewComboBoxState(2)
)

func (c *GlobalConfig) DrawFilesUI() {
	positionConfig = c.PositionConfigs[c.ActivePosition]

	if imgui.BeginTableV("GlobalFiles", 4, 0, imgui.Vec2{}, 0) {
		if positionConfig != nil {
			imgui.TableNextRow()
			imgui.TableNextColumn()
			imgui.Text("Sector file: ")
			imgui.TableNextColumn()
			imgui.Text(positionConfig.SectorFile)
			imgui.TableNextColumn()
			if imgui.Button("New...##sectorfile") {
				ui.openSectorFileDialog.Activate()
			}
			imgui.TableNextColumn()
			if positionConfig.SectorFile != "" && imgui.Button("Reload##sectorfile") {
				_ = database.LoadSectorFile(positionConfig.SectorFile)
			}

			imgui.TableNextRow()
			imgui.TableNextColumn()
			imgui.Text("Position file: ")
			imgui.TableNextColumn()
			imgui.Text(positionConfig.PositionFile)
			imgui.TableNextColumn()
			if imgui.Button("New...##positionfile") {
				ui.openPositionFileDialog.Activate()
			}
			imgui.TableNextColumn()
			if positionConfig.PositionFile != "" && imgui.Button("Reload##positionfile") {
				_ = database.LoadPositionFile(positionConfig.PositionFile)
			}
		}

		imgui.TableNextRow()
		imgui.TableNextColumn()
		imgui.Text("Aliases file: ")
		imgui.TableNextColumn()
		imgui.Text(c.AliasesFile)
		imgui.TableNextColumn()
		if imgui.Button("New...##aliasesfile") {
			ui.openAliasesFileDialog.Activate()
		}
		imgui.TableNextColumn()
		if c.AliasesFile != "" && imgui.Button("Reload##aliasesfile") {
			c.LoadAliasesFile()
		}

		imgui.TableNextRow()
		imgui.TableNextColumn()
		imgui.Text("Notes file: ")
		imgui.TableNextColumn()
		imgui.Text(c.NotesFile)
		imgui.TableNextColumn()
		if imgui.Button("New...##notesfile") {
			ui.openNotesFileDialog.Activate()
		}
		imgui.TableNextColumn()
		if c.NotesFile != "" && imgui.Button("Reload##notesfile") {
			c.LoadNotesFile()
		}

		imgui.EndTable()
	}
}

func (gc *GlobalConfig) LoadAliasesFile() {
	if gc.AliasesFile == "" {
		return
	}
	gc.aliases = make(map[string]string)

	f, err := os.Open(gc.AliasesFile)
	if err != nil {
		lg.Printf("%s: unable to read aliases file: %v", gc.AliasesFile, err)
		ShowErrorDialog("Unable to read aliases file: %v.", err)
	}
	defer f.Close()

	errors := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if len(line) == 0 || line[0] != '.' {
			continue
		}

		def := strings.SplitAfterN(line, " ", 2)
		lg.Errorf("%s -> %d %+v", line, len(def), def)
		if len(def) != 2 {
			errors += def[0] + ": no alias definition found\n"
			continue
		}

		def[0] = strings.TrimSpace(def[0])
		if _, ok := gc.aliases[def[0]]; ok {
			errors += def[0] + ": multiple definitions in alias file\n"
			// but continue and keep the latter one...
		}

		gc.aliases[def[0]] = def[1]
	}

	if len(errors) > 0 {
		ShowErrorDialog("Errors found in alias file:\n%s", errors)
	}
}

func (gc *GlobalConfig) LoadNotesFile() {
	if gc.NotesFile == "" {
		return
	}

	notes, err := os.ReadFile(gc.NotesFile)
	if err != nil {
		lg.Printf("%s: unable to read notes file: %v", gc.NotesFile, err)
		ShowErrorDialog("Unable to read notes file: %v.", err)
	} else {
		gc.notesRoot = parseNotes(string(notes))
	}
}

func configFilePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		lg.Errorf("Unable to find user config dir: %v", err)
		dir = "."
	}

	dir = path.Join(dir, "Avian")
	err = os.MkdirAll(dir, 0o700)
	if err != nil {
		lg.Errorf("%s: unable to make directory for config file: %v", dir, err)
	}

	return path.Join(dir, "config.json")
}

func (gc *GlobalConfig) Encode(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "    ")
	return enc.Encode(gc)
}

func (c *GlobalConfig) Save() error {
	lg.Printf("Saving config to: %s", configFilePath())
	f, err := os.Create(configFilePath())
	if err != nil {
		return err
	}
	defer f.Close()

	return c.Encode(f)
}

func (gc *GlobalConfig) MakeConfigActive(name string) {
	if globalConfig.PositionConfigs == nil {
		globalConfig.PositionConfigs = make(map[string]*PositionConfig)
	}
	if len(globalConfig.PositionConfigs) == 0 {
		name = "Default"
		globalConfig.PositionConfigs["Default"] = NewPositionConfig()
	}

	oldConfig := positionConfig

	// NOTE: do not be clever and try to skip this work if
	// ActivePosition==name already; this function used e.g. when the color
	// scheme changes and we need to reset everything derived from that.
	gc.ActivePosition = name
	var ok bool
	if positionConfig, ok = gc.PositionConfigs[name]; !ok {
		lg.Errorf("%s: unknown position config!", name)
		return
	}

	positionConfig.Activate()
	if oldConfig != nil && oldConfig != positionConfig {
		oldConfig.Deactivate()
	}

	if oldConfig != nil && positionConfig.SectorFile != oldConfig.SectorFile {
		if err := database.LoadSectorFile(positionConfig.SectorFile); err != nil {
			lg.Errorf("%v", err)
		}
	}
	if oldConfig != nil && positionConfig.PositionFile != oldConfig.PositionFile {
		if err := database.LoadPositionFile(positionConfig.PositionFile); err != nil {
			lg.Errorf("%v", err)
		}
	}

	wmActivateNewConfig(oldConfig, positionConfig)

	cs := positionConfig.GetColorScheme()

	uiUpdateColorScheme(cs)
	database.SetColorScheme(cs)
}

func (pc *PositionConfig) Activate() {
	if pc.eventsId == InvalidEventSubscriberId {
		pc.eventsId = eventStream.Subscribe()
	}
	if pc.sessionDrawVORs == nil {
		pc.sessionDrawVORs = make(map[string]interface{})
	}
	if pc.sessionDrawNDBs == nil {
		pc.sessionDrawNDBs = make(map[string]interface{})
	}
	if pc.sessionDrawFixes == nil {
		pc.sessionDrawFixes = make(map[string]interface{})
	}
	if pc.sessionDrawAirports == nil {
		pc.sessionDrawAirports = make(map[string]interface{})
	}
}

func (pc *PositionConfig) Deactivate() {
	eventStream.Unsubscribe(pc.eventsId)
	pc.eventsId = InvalidEventSubscriberId
}

func (pc *PositionConfig) SendUpdates() {
}

func NewPositionConfig() *PositionConfig {
	c := &PositionConfig{}

	// Give the user a semi-useful default configuration.
	c.DisplayRoot = &DisplayNode{
		SplitLine: SplitLine{Pos: 0.15, Axis: SplitAxisY},
		Children: [2]*DisplayNode{
			&DisplayNode{
				SplitLine: SplitLine{Pos: 0.7, Axis: SplitAxisX},
				Children: [2]*DisplayNode{
					&DisplayNode{Pane: NewCLIPane()},
					&DisplayNode{Pane: NewFlightPlanPane()},
				},
			},
			&DisplayNode{Pane: NewRadarScopePane("Main Scope")},
		},
	}
	c.DisplayRoot.VisitPanes(func(p Pane) { p.Activate() })

	c.ColorSchemeName = SortedMapKeys(builtinColorSchemes)[0]

	return c
}

func (c *PositionConfig) GetColorScheme() *ColorScheme {
	if cs, ok := builtinColorSchemes[c.ColorSchemeName]; ok {
		return cs
	} else if cs, ok := globalConfig.ColorSchemes[c.ColorSchemeName]; !ok {
		lg.Printf("%s: color scheme unknown; returning default", c.ColorSchemeName)
		c.ColorSchemeName = SortedMapKeys(builtinColorSchemes)[0]
		return builtinColorSchemes[c.ColorSchemeName]
	} else {
		return cs
	}
}

func (c *PositionConfig) Duplicate() *PositionConfig {
	nc := &PositionConfig{}
	*nc = *c
	nc.DisplayRoot = c.DisplayRoot.Duplicate()

	nc.eventsId = InvalidEventSubscriberId

	// don't copy the todos or timers
	return nc
}

var (
	//go:embed resources/default-config.json
	defaultConfig string
)

func LoadOrMakeDefaultConfig() {
	fn := configFilePath()
	lg.Printf("Loading config from: %s", fn)

	config, err := os.ReadFile(fn)
	if err != nil {
		config = []byte(defaultConfig)
		if errors.Is(err, os.ErrNotExist) {
			lg.Printf("%s: config file doesn't exist", fn)
			_ = os.WriteFile(fn, config, 0o600)
		} else {
			lg.Printf("%s: unable to read config file: %v", fn, err)
			ShowErrorDialog("Unable to read config file: %v\nUsing default configuration.", err)
			fn = "default.config"
		}
	}

	r := bytes.NewReader(config)
	d := json.NewDecoder(r)

	globalConfig = &GlobalConfig{}
	if err := d.Decode(globalConfig); err != nil {
		ShowErrorDialog("Configuration file is corrupt: %v", err)
	}

	globalConfig.LoadAliasesFile()
	globalConfig.LoadNotesFile()

	imgui.LoadIniSettingsFromMemory(globalConfig.ImGuiSettings)
}

func parseNotes(text string) *NotesNode {
	root := &NotesNode{}
	var hierarchy []*NotesNode
	hierarchy = append(hierarchy, root)

	for _, line := range strings.Split(text, "\n") {
		depth := 0
		for depth < len(line) && line[depth] == '*' {
			depth++
		}

		current := hierarchy[len(hierarchy)-1]
		isHeader := depth > 0
		if !isHeader {
			if len(current.text) == 0 && strings.TrimSpace(line) == "" {
				// drop leading blank lines
			} else {
				current.text = append(current.text, line)
			}
			continue
		}

		// We're done with the text for this node; drop any trailing lines
		// in the text that are purely whitespace.
		for i := len(current.text) - 1; i > 0; i-- {
			if strings.TrimSpace(current.text[i]) == "" {
				current.text = current.text[:i]
			} else {
				break
			}
		}

		for depth > len(hierarchy) {
			hierarchy = append(hierarchy, &NotesNode{})
			n := len(hierarchy)
			hierarchy[n-2].children = append(hierarchy[n-2].children, hierarchy[n-1])
		}

		newNode := &NotesNode{title: strings.TrimSpace(line[depth:])}
		if depth == len(hierarchy) {
			hierarchy = append(hierarchy, newNode)
		} else {
			hierarchy[depth] = newNode
			hierarchy = hierarchy[:depth+1]
		}
		n := len(hierarchy)
		hierarchy[n-2].children = append(hierarchy[n-2].children, newNode)
	}

	return root
}

func (pc *PositionConfig) Update() {
	for _, event := range eventStream.Get(pc.eventsId) {
		if sel, ok := event.(*SelectedAircraftEvent); ok {
			pc.selectedAircraft = sel.ac
		}
	}
}
