// The settings people choose for each printer: whether the assist is on, and
// how gentle the first layer is. They are one JSON file in the data directory
// the operator grants, shared by everyone who opens a printer's page.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// PrinterSettings is how the assist treats one printer.
type PrinterSettings struct {
	// Enabled switches the assist on for this printer.
	Enabled bool `json:"enabled"`
	// Speed is the first layer's speed, in percent of the print's own.
	Speed int `json:"speed"`
	// BedOffset is how many °C warmer the bed is for the first layer.
	BedOffset int `json:"bed_offset"`
	// RampLayers is over how many layers the speed climbs back after the
	// first one.
	RampLayers int `json:"ramp_layers"`
}

// The bounds a person may choose within, and the defaults: what a person who
// does this by hand in the Tune menu tends to pick.
const (
	minSpeed, maxSpeed, defaultSpeed = 30, 100, 60
	maxBedOffset, defaultBedOffset   = 15, 5
	minRamp, maxRamp, defaultRamp    = 1, 10, 3
)

// Defaults is what a printer nobody set up yet gets, switched off.
func Defaults() PrinterSettings {
	return PrinterSettings{Speed: defaultSpeed, BedOffset: defaultBedOffset, RampLayers: defaultRamp}
}

var errInvalid = errors.New("invalid")

func (s PrinterSettings) validate() error {
	switch {
	case s.Speed < minSpeed || s.Speed > maxSpeed:
		return fmt.Errorf("%w: the first layer's speed is %d %%, it has to be %d to %d %%", errInvalid, s.Speed, minSpeed, maxSpeed)
	case s.BedOffset < 0 || s.BedOffset > maxBedOffset:
		return fmt.Errorf("%w: the bed is %d °C warmer, it has to be 0 to %d °C", errInvalid, s.BedOffset, maxBedOffset)
	case s.RampLayers < minRamp || s.RampLayers > maxRamp:
		return fmt.Errorf("%w: the speed climbs back over %d layers, it has to be %d to %d", errInvalid, s.RampLayers, minRamp, maxRamp)
	}

	return nil
}

const maxPrinters = 5000

// settingsFile keeps every printer's settings. A read and a change hold the
// lock for the whole read-merge-write.
type settingsFile struct {
	lock sync.Mutex
	dir  string
	// cached is the file as last read or written, so the loop that runs every
	// few seconds does not read the disk each time.
	cached map[string]PrinterSettings
}

func (f *settingsFile) path() string { return filepath.Join(f.dir, "settings.json") }

// of answers one printer's settings, or the defaults.
func (f *settingsFile) of(id string) (PrinterSettings, error) {
	all, err := f.all()
	if err != nil {
		return PrinterSettings{}, err
	}

	if s, ok := all[id]; ok {
		return s, nil
	}

	return Defaults(), nil
}

// all answers every printer's settings. The map is the caller's.
func (f *settingsFile) all() (map[string]PrinterSettings, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	if err := f.load(); err != nil {
		return nil, err
	}

	out := make(map[string]PrinterSettings, len(f.cached))
	for id, s := range f.cached {
		out[id] = s
	}

	return out, nil
}

// set saves one printer's settings.
func (f *settingsFile) set(id string, s PrinterSettings) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	if err := f.load(); err != nil {
		return err
	}

	next := make(map[string]PrinterSettings, len(f.cached)+1)
	for k, v := range f.cached {
		next[k] = v
	}

	next[id] = s

	if len(next) > maxPrinters {
		return fmt.Errorf("%w: settings for more than %d printers", errInvalid, maxPrinters)
	}

	if err := replaceFile(f.path(), next); err != nil {
		return err
	}

	f.cached = next

	return nil
}

// load reads the file once. A missing file is no printer set up yet. A file
// that is not settings (one a crash cut short) reads the same way, so the next
// change repairs it.
func (f *settingsFile) load() error {
	if f.cached != nil {
		return nil
	}

	raw, err := os.ReadFile(f.path())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f.cached = map[string]PrinterSettings{}

		return nil
	case err != nil:
		return fmt.Errorf("read %s: %w", f.path(), err)
	}

	var all map[string]PrinterSettings
	if err = json.Unmarshal(raw, &all); err != nil || all == nil {
		fmt.Fprintf(os.Stderr, "%s: %s holds no settings, every printer reads as switched off: %v\n", name, f.path(), err)

		all = map[string]PrinterSettings{}
	}

	f.cached = all

	return nil
}

// replaceFile writes value as JSON to target in one step: to a sibling file,
// flushed to the disk, then renamed over target, so a crash leaves either the
// old file or the new one.
func replaceFile(target string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", target, err)
	}

	next := target + ".next"

	out, err := os.OpenFile(next, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", next, err)
	}

	_, err = out.Write(raw)
	if err == nil {
		err = out.Sync()
	}

	if closeErr := out.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(next, target)
	}

	if err != nil {
		_ = os.Remove(next)

		return fmt.Errorf("save %s: %w", target, err)
	}

	return nil
}
