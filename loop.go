// The loop that looks at every printer every few seconds and sends what the
// assist decides. It runs as the module itself: reading the fleet takes
// observe, and changing a printer's speed and bed temperature takes operate on
// that printer, which switching the assist on for it grants.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
)

// lookEvery is how often the loop reads the fleet. A printer reports about
// every 5 seconds, so this sees each report soon after it lands.
const lookEvery = 2 * time.Second

// Note says why the assist cannot act on a printer.
type Note string

const (
	// NoteUnsupported: the printer does not take a speed or bed command.
	NoteUnsupported Note = "unsupported"
	// NoteDenied: the module may not operate this printer. Switching the
	// assist on grants that; an administrator may have taken it back.
	NoteDenied Note = "denied"
	// NoteRefused: the printer refused a command, MachineReason says why.
	NoteRefused Note = "refused"
)

// PrinterStatus is what the page shows about one printer.
type PrinterStatus struct {
	Status
	Note   Note   `json:"note,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// look reads the fleet once and steps every printer's assist.
func (a *app) look(ctx context.Context) {
	file := a.settingsFile()
	if file == nil {
		return
	}

	all, err := file.all()
	if err != nil {
		a.log.WarnContext(ctx, "the settings could not be read", "error", err)

		return
	}

	devices, err := a.listAll(ctx, "")
	if err != nil {
		a.log.WarnContext(ctx, "the fleet could not be read", "error", err)

		return
	}

	seen := make(map[string]bool, len(devices))

	for _, d := range devices {
		seen[d.ID] = true
		settings, ok := all[d.ID]
		if !ok {
			settings = Defaults()
		}

		a.stepPrinter(ctx, d, settings)
	}

	a.mu.Lock()
	for id := range a.trackers {
		if !seen[id] {
			delete(a.trackers, id)
			delete(a.notes, id)
		}
	}
	a.mu.Unlock()
}

func (a *app) stepPrinter(ctx context.Context, d deviceapi.Device, settings PrinterSettings) {
	a.mu.Lock()
	t, ok := a.trackers[d.ID]
	if !ok {
		t = &tracker{phase: PhaseIdle}
		a.trackers[d.ID] = t
	}

	supported := slices.Contains(d.Capabilities, verbSetSpeed) && slices.Contains(d.Capabilities, verbSetBed)

	// An offline printer lists no capabilities, so it waits rather than
	// reading as one that cannot take the commands.
	if !d.Connected || !supported {
		t.reset()
		if d.Connected {
			a.notes[d.ID] = PrinterStatus{Note: NoteUnsupported}
		} else {
			delete(a.notes, d.ID)
		}
		a.mu.Unlock()

		return
	}

	actions := t.step(readingOf(d, a.now()), settings)
	a.mu.Unlock()

	for _, act := range actions {
		a.send(ctx, d, act)
	}
}

// send gives one command to the printer as the module, and keeps why it
// failed for the page.
func (a *app) send(ctx context.Context, d deviceapi.Device, act Action) {
	result, err := a.fleet.Command(ctx, deviceapi.CommandParams{ID: d.ID, Verb: act.Verb, Args: act.Args})

	note := PrinterStatus{}

	switch {
	case errors.Is(err, deviceapi.ErrDenied) || errors.Is(err, deviceapi.ErrNoDevice):
		note.Note = NoteDenied
	case err != nil:
		note.Note, note.Reason = NoteRefused, err.Error()
	case !result.OK:
		note.Note, note.Reason = NoteRefused, result.MachineReason
	}

	if note.Note != "" {
		a.log.WarnContext(ctx, "a command was not carried out", "printer", d.ID, "verb", act.Verb,
			"args", act.Args, "note", note.Note, "reason", note.Reason)
	} else {
		a.log.InfoContext(ctx, "a command was sent", "printer", d.ID, "verb", act.Verb, "args", act.Args)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if note.Note == "" {
		delete(a.notes, d.ID)
	} else {
		a.notes[d.ID] = note
	}
}

// statusOf answers what the page shows about one printer.
func (a *app) statusOf(id string) PrinterStatus {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := a.notes[id]
	if t, ok := a.trackers[id]; ok {
		out.Status = t.status()
	} else {
		out.Status = Status{Phase: PhaseIdle}
	}

	return out
}

// listAll reads every printer the principal may see, page by page.
func (a *app) listAll(ctx context.Context, principal string) ([]deviceapi.Device, error) {
	var out []deviceapi.Device

	for offset := 0; ; {
		page, err := a.fleet.List(ctx, deviceapi.ListParams{Principal: principal, Limit: deviceapi.MaxPageLimit, Offset: offset})
		if err != nil {
			return nil, fmt.Errorf("list the printers: %w", err)
		}

		out = append(out, page.Items...)
		offset += len(page.Items)

		if len(page.Items) == 0 || offset >= page.Total || len(out) >= maxPrinters {
			return out, nil
		}
	}
}

// readingOf takes what the assist reads out of a printer's state tree.
func readingOf(d deviceapi.Device, at time.Time) Reading {
	r := Reading{At: at}
	r.State, _ = text(d.State, "/state")
	r.Job, _ = text(d.State, "/job/id")
	r.Z, r.HasZ = number(d.State, "/axis/z")

	if speed, ok := number(d.State, "/speed"); ok {
		r.Speed = int(speed)
	}

	r.BedTemp, _ = number(d.State, "/temperature/bed")
	r.BedTarget, _ = number(d.State, "/temperature/bed_target")

	tool := activeTool(d.State)
	r.NozzleTemp, _ = number(d.State, "/tools/"+tool+"/nozzle_temp")
	r.NozzleTarget, _ = number(d.State, "/tools/"+tool+"/nozzle_target_temp")

	return r
}

// activeTool names the head that prints now: the one marked active, else the
// first.
func activeTool(state map[string]json.RawMessage) string {
	for key, raw := range state {
		tool, ok := strings.CutPrefix(key, "/tools/")
		if !ok {
			continue
		}

		id, rest, ok := strings.Cut(tool, "/")
		if !ok || rest != "active" {
			continue
		}

		var active bool
		if json.Unmarshal(raw, &active) == nil && active {
			return id
		}
	}

	return "0"
}

func number(state map[string]json.RawMessage, key string) (float64, bool) {
	raw, ok := state[key]
	if !ok {
		return 0, false
	}

	var v float64
	if err := json.Unmarshal(raw, &v); err == nil {
		return v, true
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, err = strconv.ParseFloat(s, 64); err == nil {
			return v, true
		}
	}

	return 0, false
}

// text reads a value as text: a string as it is, a number in its shortest form.
func text(state map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := state[key]
	if !ok {
		return "", false
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}

	if v, ok := number(state, key); ok {
		return strconv.FormatFloat(v, 'f', -1, 64), true
	}

	return strings.TrimSpace(string(raw)), true
}
