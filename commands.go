// commands.go
// Copyright(c) 2022 Matt Pharr, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

// This file defines functionality related to built-in commands in vice,
// including both function-key based and in the CLI.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"
)

///////////////////////////////////////////////////////////////////////////
// CLICommands

// There is admittedly some redundancy between CLICommands and
// FKeyCommands--"I take these parameters", "here are the parameters, now
// run the command", etc. It would be nice to unify them at some point...

type CLICommand interface {
	Names() []string
	Help() string
	Usage() string
	TakesAircraft() bool
	TakesController() bool
	AdditionalArgs() (min int, max int)
	Run(cmd string, ac *Aircraft, ctrl *Controller, args []string, cli *CLIPane) []*ConsoleEntry
}

var (
	cliCommands []CLICommand = []CLICommand{
		&NYPRDCommand{},
		&PRDCommand{},
		&FindCommand{},
		&DrawRouteCommand{},
		&FlagAircraftCommand{},
		&InfoCommand{},
	}
)

func checkCommands(cmds []CLICommand) {
	seen := make(map[string]interface{})
	for _, c := range cmds {
		for _, name := range c.Names() {
			if _, ok := seen[name]; ok {
				lg.Errorf("%s: command has multiple definitions", name)
			} else {
				seen[name] = nil
			}
		}
	}
}

type NYPRDEntry struct {
	Id            int       `json:"id"`
	AirportOrigin string    `json:"airport_origin"`
	AirportDest   string    `json:"airport_dest"`
	Route         string    `json:"route"`
	Hours1        string    `json:"hours1"`
	Hours2        string    `json:"hours2"`
	Hours3        string    `json:"hours3"`
	RouteType     string    `json:"route_type"`
	Area          string    `json:"area"`
	Altitude      string    `json:"altitude"`
	Aircraft      string    `json:"aircraft"`
	Direction     string    `json:"direction"`
	Seq           string    `json:"seq"`
	CenterOrigin  string    `json:"center_origin"`
	CenterDest    string    `json:"center_dest"`
	IsLocal       int       `json:"is_local"`
	Created       time.Time `json:"created_at"`
	Updated       time.Time `json:"updated_at"`
}

type NYPRDCommand struct{}

func (*NYPRDCommand) Names() []string                    { return []string{"nyprd"} }
func (*NYPRDCommand) Usage() string                      { return "" }
func (*NYPRDCommand) TakesAircraft() bool                { return true }
func (*NYPRDCommand) TakesController() bool              { return false }
func (*NYPRDCommand) AdditionalArgs() (min int, max int) { return 0, 2 }
func (*NYPRDCommand) Help() string {
	return "Looks up the aircraft's route in the ZNY preferred route database."
}
func (*NYPRDCommand) Run(cmd string, ac *Aircraft, ctrl *Controller, args []string, cli *CLIPane) []*ConsoleEntry {
	var depart, arrive string
	if len(args) > 0 {
		if len(args) == 2 {
			depart, arrive = args[0], args[1]
		} else {
			return ErrorStringConsoleEntry("nyprd: expected two airports")
		}
	} else if ac != nil {
		if ac.FlightPlan == nil {
			return ErrorConsoleEntry(ErrNoFlightPlan)
		}
		depart, arrive = ac.FlightPlan.DepartureAirport, ac.FlightPlan.ArrivalAirport
	} else {
		return ErrorStringConsoleEntry("nyprd: must select an aircraft or provide two airports")
	}

	url := fmt.Sprintf("https://nyartcc.org/prd/search?depart=%s&arrive=%s", depart, arrive)

	resp, err := http.Get(url)
	if err != nil {
		lg.Printf("PRD get err: %+v", err)
		return ErrorStringConsoleEntry("nyprd: network error")
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	var prdEntries []NYPRDEntry
	if err := decoder.Decode(&prdEntries); err != nil {
		lg.Errorf("PRD decode err: %+v", err)
		return ErrorStringConsoleEntry("error decoding PRD entry")
	}

	if len(prdEntries) == 0 {
		return ErrorStringConsoleEntry(fmt.Sprintf("no PRD found for route from %s to %s", depart, arrive))
	}

	anyType := false
	anyArea := false
	anyAlt := false
	anyAC := false
	for _, entry := range prdEntries {
		anyType = anyType || (entry.RouteType != "")
		anyArea = anyArea || (entry.Area != "")
		anyAlt = anyAlt || (entry.Altitude != "")
		anyAC = anyAC || (entry.Aircraft != "")
	}

	var result strings.Builder
	w := tabwriter.NewWriter(&result, 0 /* min width */, 1 /* tab width */, 1 /* padding */, ' ', 0)
	w.Write([]byte("\tORG\tDST\t"))
	writeIf := func(b bool, s string) {
		if b {
			w.Write([]byte(s))
		}
	}

	writeIf(anyType, "TYPE\t")
	writeIf(anyArea, "AREA\t")
	writeIf(anyAlt, "ALT\t")
	writeIf(anyAC, "A/C\t")
	w.Write([]byte("ROUTE\n"))

	print := func(entry NYPRDEntry) {
		w.Write([]byte(entry.AirportOrigin + "\t" + entry.AirportDest + "\t"))
		writeIf(anyType, entry.RouteType+"\t")
		writeIf(anyArea, entry.Area+"\t")
		writeIf(anyAlt, entry.Altitude+"\t")
		writeIf(anyAC, entry.Aircraft+"\t")
		w.Write([]byte(entry.Route + "\n"))
	}

	// Print the required ones first, with an asterisk
	for _, entry := range prdEntries {
		if entry.IsLocal == 0 {
			continue
		}
		w.Write([]byte("*\t"))
		print(entry)
	}
	for _, entry := range prdEntries {
		if entry.IsLocal != 0 {
			continue
		}
		w.Write([]byte("\t"))
		print(entry)
	}
	w.Flush()

	return StringConsoleEntry(result.String())
}

type PRDCommand struct{}

func (*PRDCommand) Names() []string                    { return []string{"faaprd"} }
func (*PRDCommand) Usage() string                      { return "" }
func (*PRDCommand) TakesAircraft() bool                { return true }
func (*PRDCommand) TakesController() bool              { return false }
func (*PRDCommand) AdditionalArgs() (min int, max int) { return 0, 0 }
func (*PRDCommand) Help() string {
	return "Looks up the aircraft's route in the FAA preferred route database."
}
func (*PRDCommand) Run(cmd string, ac *Aircraft, ctrl *Controller, args []string, cli *CLIPane) []*ConsoleEntry {
	var depart, arrive string
	if len(args) > 0 {
		if len(args) == 2 {
			depart, arrive = args[0], args[1]
		} else {
			return ErrorStringConsoleEntry("nyprd: expected two airports")
		}
	} else if ac != nil {
		if ac.FlightPlan == nil {
			return ErrorConsoleEntry(ErrNoFlightPlan)
		}

		depart, arrive = ac.FlightPlan.DepartureAirport, ac.FlightPlan.ArrivalAirport
	}

	if len(depart) == 4 && depart[0] == 'K' {
		depart = depart[1:]
	}
	if len(arrive) == 4 && arrive[0] == 'K' {
		arrive = arrive[1:]
	}

	if prdEntries, ok := database.FAA.prd[AirportPair{depart, arrive}]; !ok {
		return ErrorStringConsoleEntry(fmt.Sprintf(depart + "-" + arrive + ": no entry in FAA PRD"))
	} else {
		anyType := false
		anyHour1, anyHour2, anyHour3 := false, false, false
		anyAC := false
		anyAlt, anyDir := false, false
		for _, entry := range prdEntries {
			anyType = anyType || (entry.Type != "")
			anyHour1 = anyHour1 || (entry.Hours[0] != "")
			anyHour2 = anyHour2 || (entry.Hours[1] != "")
			anyHour3 = anyHour3 || (entry.Hours[2] != "")
			anyAC = anyAC || (entry.Aircraft != "")
			anyAlt = anyAlt || (entry.Altitude != "")
			anyDir = anyDir || (entry.Direction != "")
		}

		var result strings.Builder
		w := tabwriter.NewWriter(&result, 0 /* min width */, 1 /* tab width */, 1 /* padding */, ' ', 0)
		w.Write([]byte("NUM\tORG\tDST\t"))

		writeIf := func(b bool, s string) {
			if b {
				w.Write([]byte(s))
			}
		}
		writeIf(anyType, "TYPE\t")
		writeIf(anyHour1, "HOUR1\t")
		writeIf(anyHour2, "HOUR2\t")
		writeIf(anyHour3, "HOUR3\t")
		writeIf(anyAC, "A/C\t")
		writeIf(anyAlt, "ALT\t")
		writeIf(anyDir, "DIR\t")
		w.Write([]byte("ROUTE\n"))

		for _, entry := range prdEntries {
			w.Write([]byte(entry.Seq + "\t" + entry.Depart + "\t" + entry.Arrive + "\t"))
			writeIf(anyType, entry.Type+"\t")
			writeIf(anyHour1, entry.Hours[0]+"\t")
			writeIf(anyHour2, entry.Hours[1]+"\t")
			writeIf(anyHour3, entry.Hours[2]+"\t")
			writeIf(anyAC, entry.Aircraft+"\t")
			writeIf(anyAlt, entry.Altitude+"\t")
			writeIf(anyDir, entry.Direction+"\t")
			w.Write([]byte(entry.Route + "\n"))
		}
		w.Flush()

		return StringConsoleEntry(result.String())
	}
}

type FindCommand struct{}

func (*FindCommand) Names() []string { return []string{"find"} }
func (*FindCommand) Usage() string {
	return "<callsign, fix, VOR, DME, airport...>"
}
func (*FindCommand) TakesAircraft() bool                { return false }
func (*FindCommand) TakesController() bool              { return false }
func (*FindCommand) AdditionalArgs() (min int, max int) { return 1, 1 }
func (*FindCommand) Help() string {
	return "Finds the specified object and highlights it in any radar scopes in which it is visible."
}
func (*FindCommand) Run(cmd string, ac *Aircraft, ctrl *Controller, args []string, cli *CLIPane) []*ConsoleEntry {
	var pos Point2LL
	name := strings.ToUpper(args[0])
	aircraft := matchingAircraft(name)
	if len(aircraft) == 1 {
		pos = aircraft[0].Position()
	} else if len(aircraft) > 1 {
		callsigns := MapSlice(aircraft, func(a *Aircraft) string { return a.Callsign })
		return ErrorStringConsoleEntry("Multiple aircraft match: " + strings.Join(callsigns, ", "))
	} else {
		var ok bool
		if pos, ok = database.Locate(name); !ok {
			return ErrorStringConsoleEntry(args[0] + ": no matches found")
		}
	}

	positionConfig.highlightedLocation = pos
	positionConfig.highlightedLocationEndTime = time.Now().Add(3 * time.Second)
	return nil
}

type DrawRouteCommand struct{}

func (*DrawRouteCommand) Names() []string                    { return []string{"drawroute", "dr"} }
func (*DrawRouteCommand) Usage() string                      { return "" }
func (*DrawRouteCommand) TakesAircraft() bool                { return true }
func (*DrawRouteCommand) TakesController() bool              { return false }
func (*DrawRouteCommand) AdditionalArgs() (min int, max int) { return 0, 0 }
func (*DrawRouteCommand) Help() string {
	return "Draws the route of the specified aircraft in any radar scopes in which it is visible."
}
func (*DrawRouteCommand) Run(cmd string, ac *Aircraft, ctrl *Controller, args []string, cli *CLIPane) []*ConsoleEntry {
	if ac == nil {
		return ErrorStringConsoleEntry("drawroute: must select aircraft")
	}

	if ac.FlightPlan == nil {
		return ErrorConsoleEntry(ErrNoFlightPlan)
	} else {
		positionConfig.drawnRoute = ac.FlightPlan.DepartureAirport + " " + ac.FlightPlan.Route + " " +
			ac.FlightPlan.ArrivalAirport
		positionConfig.drawnRouteEndTime = time.Now().Add(5 * time.Second)
		return nil
	}
}

type FlagAircraftCommand struct{}

func (*FlagAircraftCommand) Names() []string                    { return []string{"f", "flag"} }
func (*FlagAircraftCommand) Usage() string                      { return "" }
func (*FlagAircraftCommand) TakesAircraft() bool                { return false }
func (*FlagAircraftCommand) TakesController() bool              { return false }
func (*FlagAircraftCommand) AdditionalArgs() (min int, max int) { return 1, 1 }
func (*FlagAircraftCommand) Help() string {
	return "Toggle whether an aircraft is flagged"
}
func (*FlagAircraftCommand) Run(cmd string, ac *Aircraft, ctrl *Controller, args []string, cli *CLIPane) []*ConsoleEntry {
	name := strings.ToUpper(args[0])
	aircraft := matchingAircraft(name)
	if len(aircraft) == 1 {
		positionConfig.ToggleFlagged(aircraft[0].Callsign)
		return nil
	} else if len(aircraft) > 1 {
		callsigns := MapSlice(aircraft, func(a *Aircraft) string { return a.Callsign })
		return ErrorStringConsoleEntry("Multiple aircraft match: " + strings.Join(callsigns, ", "))
	} else {
		return ErrorStringConsoleEntry("No matching aircraft")
	}
}

type InfoCommand struct{}

func (*InfoCommand) Names() []string { return []string{"i", "info"} }
func (*InfoCommand) Usage() string {
	return "<callsign, fix, VOR, DME, airport...>"
}
func (*InfoCommand) TakesAircraft() bool                { return false }
func (*InfoCommand) TakesController() bool              { return false }
func (*InfoCommand) AdditionalArgs() (min int, max int) { return 0, 1 }

func (*InfoCommand) Help() string {
	return "Prints available information about the specified object."
}
func (*InfoCommand) Run(cmd string, ac *Aircraft, ctrl *Controller, args []string, cli *CLIPane) []*ConsoleEntry {
	acInfo := func(ac *Aircraft) string {
		var result string
		var indent int
		if ac.FlightPlan == nil {
			result = ac.Callsign + ": no flight plan filed"
			indent = len(ac.Callsign) + 1
		} else {
			result, indent = ac.GetFormattedFlightPlan(true)
			result = strings.TrimRight(result, "\n")
		}

		indstr := fmt.Sprintf("%*c", indent, ' ')
		if u := server.GetUser(ac.Callsign); u != nil {
			result += fmt.Sprintf("\n%spilot: %s %s (%s)", indstr, u.Name, u.Rating, u.Note)
		}
		if ac.FlightPlan != nil {
			if tel := ac.Telephony(); tel != "" {
				result += fmt.Sprintf("\n%stele:  %s", indstr, tel)
			}

			if a, ok := database.LookupAircraftType(ac.FlightPlan.BaseType()); ok {
				result += fmt.Sprintf("\n%stype:  %s", indstr, a.Name)
			}
		}
		if ac.TrackingController != "" {
			result += fmt.Sprintf("\n%sctrl:  %s", indstr, ac.TrackingController)
		}
		if ac.InboundHandoffController != "" {
			result += fmt.Sprintf("\n%sin h/o:  %s", indstr, ac.InboundHandoffController)
		}
		if ac.OutboundHandoffController != "" {
			result += fmt.Sprintf("\n%sout h/o: %s", indstr, ac.OutboundHandoffController)
		}
		if ac.FlightPlan != nil {
			if acType, ok := database.LookupAircraftType(ac.FlightPlan.BaseType()); ok {
				result += fmt.Sprintf("\n%stype:  %d engine %s (%s)", indstr, acType.NumEngines(),
					acType.EngineType(), acType.Manufacturer)
				result += fmt.Sprintf("\n%sappr:  %s", indstr, acType.ApproachCategory())
				result += fmt.Sprintf("\n%srecat: %s", indstr, acType.RECATCategory())
			}
		}
		if ac.HaveTrack() {
			result += fmt.Sprintf("\n%scralt: %d", indstr, ac.Altitude())
		}
		result += fmt.Sprintf("\n%shours: %d", indstr, int(ac.HoursOnNetwork(true)))
		if ac.Squawk != ac.AssignedSquawk {
			result += fmt.Sprintf("\n%s*** Actual squawk: %s", indstr, ac.Squawk)
		}
		if ac.LostTrack(server.CurrentTime()) {
			result += fmt.Sprintf("\n%s*** Lost Track!", indstr)
		}
		return result
	}

	if len(args) == 1 {
		name := strings.ToUpper(args[0])

		// e.g. "fft" matches both a VOR and a callsign, so report both...
		var info []string
		if navaid, ok := database.FAA.navaids[name]; ok {
			info = append(info, fmt.Sprintf("%s: %s %s %s", name, stopShouting(navaid.Name),
				navaid.Type, navaid.Location.DMSString()))
		}
		if fix, ok := database.FAA.fixes[name]; ok {
			info = append(info, fmt.Sprintf("%s: Fix %s", name, fix.Location.DMSString()))
		}
		if ap, ok := database.airports[name]; ok {
			info = append(info, fmt.Sprintf("%s: %s: %s, alt %d", name, stopShouting(ap.Name),
				ap.Location.DMSString(), ap.Elevation))
		}
		if cs, ok := database.callsigns[name]; ok {
			info = append(info, fmt.Sprintf("%s: %s (%s)", name, cs.Telephony, cs.Company))
		}
		if ct := server.GetController(name); ct != nil {
			info = append(info, fmt.Sprintf("%s (%s) @ %s, range %d", ct.Callsign,
				ct.Rating, ct.Frequency.String(), ct.ScopeRange))
			_ = server.RequestControllerATIS(name)
			if u := server.GetUser(name); u != nil {
				info = append(info, fmt.Sprintf("%s %s (%s)", u.Name, u.Rating, u.Note))
			}
		}
		if ac, ok := database.LookupAircraftType(name); ok {
			indent := fmt.Sprintf("%*c", len(ac.Name)+2, ' ')
			info = append(info, fmt.Sprintf("%s: %d engine %s (%s)",
				ac.Name, ac.NumEngines(), ac.EngineType(), ac.Manufacturer))
			info = append(info, indent+"Approach: "+ac.ApproachCategory())
			info = append(info, indent+"RECAT: "+ac.RECATCategory())
		}

		if len(info) > 0 {
			return StringConsoleEntry(strings.Join(info, "\n"))
		}

		aircraft := matchingAircraft(name)
		if len(aircraft) == 1 {
			return StringConsoleEntry(acInfo(aircraft[0]))
		} else if len(aircraft) > 1 {
			callsigns := MapSlice(aircraft, func(a *Aircraft) string { return a.Callsign })
			return ErrorStringConsoleEntry("Multiple aircraft match: " + strings.Join(callsigns, ", "))
		} else {
			return ErrorStringConsoleEntry(name + ": unknown")
		}
	} else if positionConfig.selectedAircraft != nil {
		return StringConsoleEntry(acInfo(positionConfig.selectedAircraft))
	} else {
		return ErrorStringConsoleEntry(cmd + ": must either specify a fix/VOR/etc. or select an aircraft")
	}
}
