// The first-layer assist for one printer: what a person does in the printer's
// Tune menu, done for them. When a print reaches its first layer, the speed
// drops (60 % by default) and the bed goes a few degrees warmer. Once the first
// layer is down, the bed goes back and the speed climbs back to where it was
// over the next few layers.
//
// It reads only what the printer reports about itself: its state, the height
// of the nozzle, the speed factor, and the temperatures and their targets. It
// never reads the G-code, so it does not matter which slicer made the file.
package main

import (
	"math"
	"strconv"
	"time"
)

// Phase is where the assist is in one print.
type Phase string

const (
	// PhaseIdle: the printer is not printing.
	PhaseIdle Phase = "idle"
	// PhaseWaiting: a print runs its start sequence (heating, homing, leveling).
	PhaseWaiting Phase = "waiting"
	// PhaseFirstLayer: the slower speed and the warmer bed are set.
	PhaseFirstLayer Phase = "first_layer"
	// PhaseRamping: the first layer is down, the speed climbs back.
	PhaseRamping Phase = "ramping"
	// PhaseDone: everything is back where it was.
	PhaseDone Phase = "done"
	// PhaseSkipped: the print was past its first layer when the assist saw it.
	PhaseSkipped Phase = "skipped"
)

const (
	// zWindow is how far back the lowest reported height is taken. A printer
	// reports about every 5 seconds, and a travel move lifts the nozzle for a
	// moment, so the lowest height over a few reports is the layer's.
	zWindow = 12 * time.Second
	// minLayerStep and maxLayerStep bound a change of the lowest height that
	// counts as the next layer: smaller is noise, larger is a lift (a pause
	// parks the nozzle higher, so does the end of a print).
	minLayerStep = 0.05
	maxLayerStep = 1.0
	// firstLayerHeight is the highest a first layer can be, in mm.
	firstLayerHeight = 1.0
	// printTemperature is the nozzle target that marks printing, in °C. Prusa
	// start sequences level the bed with the nozzle at 170 °C or cooler, so
	// the first layer starts once the target is above this.
	printTemperature = 180
	// nearTarget is how close a temperature counts as reached, in °C.
	nearNozzle = 5
	nearBed    = 2
	// maxBed is the highest bed target the assist sets, as the command allows.
	maxBed = 120
	// reapplyLimit is how many times a value a start sequence put back is set
	// again, before the assist leaves it alone.
	reapplyLimit = 3
	// reportLag is how long a printer may go on reporting the value it had
	// before a command: it sends its state every few seconds.
	reportLag = 15 * time.Second
)

// Reading is one look at a printer.
type Reading struct {
	At    time.Time
	State string
	Job   string
	// Z is the nozzle's height in mm, as the G-code names it. HasZ is false
	// for a printer that does not report one.
	Z    float64
	HasZ bool
	// Speed is the speed factor in percent, and 0 when not reported.
	Speed int
	// Bed and Nozzle are the temperatures and their targets in °C.
	BedTemp, BedTarget       float64
	NozzleTemp, NozzleTarget float64
}

// Action is one command for the printer.
type Action struct {
	Verb string
	Args map[string]string
}

const (
	verbSetSpeed = "core.command.set_speed"
	verbSetBed   = "core.command.set_bed_temperature"
)

func setSpeed(percent int) Action {
	return Action{Verb: verbSetSpeed, Args: map[string]string{"speed": strconv.Itoa(percent)}}
}

func setBed(celsius int) Action {
	return Action{Verb: verbSetBed, Args: map[string]string{"bed_temperature": strconv.Itoa(celsius)}}
}

type zSample struct {
	at time.Time
	z  float64
}

// Status is what the page shows about one printer.
type Status struct {
	Phase Phase `json:"phase"`
	// Layer counts from 1 once the first layer started, 0 before.
	Layer int `json:"layer,omitempty"`
	// Speed is the speed the assist last set, in percent.
	Speed int `json:"speed,omitempty"`
	// BedRaised is how many °C the assist has the bed above the print's own
	// target right now.
	BedRaised int `json:"bed_raised,omitempty"`
	// SpeedTakenOver and BedTakenOver say a person changed that value
	// themselves, so the assist leaves it alone for the rest of the print.
	SpeedTakenOver bool `json:"speed_taken_over,omitempty"`
	BedTakenOver   bool `json:"bed_taken_over,omitempty"`
}

// tracker follows one printer through its prints.
type tracker struct {
	phase Phase
	job   string

	zs     []zSample
	layerZ float64
	layer  int
	// settled is when the first layer's height stops being lowered by what
	// the start sequence leaves behind (leveling, the purge line).
	settled time.Time
	// rising is when the lowest height first stood a layer higher.
	rising   time.Time
	risingAt float64

	// The values the print had before the assist changed them, and the ones
	// it set.
	speedFrom, speedSet int
	speedLow            int
	bedFrom, bedSet     int
	// speedWas and bedWas are what the printer had before the last command,
	// which it may still report until speedAt or bedAt plus reportLag.
	speedWas, bedWas    int
	speedAt, bedAt      time.Time
	speedMine, bedMine  bool
	speedAgain, bedAgain int
	speedTaken, bedTaken bool
	rampStep             int
}

// sendSpeed and sendBed set a value and remember what the printer had, which
// it may report for a little while longer.
func (t *tracker) sendSpeed(percent int, at time.Time) Action {
	t.speedWas, t.speedSet, t.speedAt = t.speedSet, percent, at

	return setSpeed(percent)
}

func (t *tracker) sendBed(celsius int, at time.Time) Action {
	t.bedWas, t.bedSet, t.bedAt = t.bedSet, celsius, at

	return setBed(celsius)
}

func (t *tracker) status() Status {
	s := Status{Phase: t.phase, Layer: t.layer, SpeedTakenOver: t.speedTaken, BedTakenOver: t.bedTaken}
	if t.phase == PhaseIdle || t.phase == PhaseWaiting {
		s.Phase = t.phase
		if s.Phase == "" {
			s.Phase = PhaseIdle
		}

		return s
	}

	if t.speedMine {
		s.Speed = t.speedSet
	}

	if t.bedMine && t.bedSet > t.bedFrom {
		s.BedRaised = t.bedSet - t.bedFrom
	}

	return s
}

func printing(state string) bool { return state == "PRINTING" }

func paused(state string) bool { return state == "PAUSED" || state == "PAUSING" || state == "RESUMING" }

// step takes one reading and answers the commands to send. The settings are
// the printer's; an assist switched off mid-print puts back what it changed.
func (t *tracker) step(r Reading, s PrinterSettings) []Action {
	if !printing(r.State) && !paused(r.State) {
		t.reset()

		return nil
	}

	if t.phase == "" || t.phase == PhaseIdle || (r.Job != "" && r.Job != t.job) {
		t.reset()
		t.job = r.Job
		t.phase = PhaseWaiting
		// A print already past its start when the assist first sees it (the
		// server restarted mid-print, or the assist was just switched on) is
		// left alone: it is too late to help its first layer.
		if r.HasZ && r.Z > firstLayerHeight && r.NozzleTarget > printTemperature {
			t.phase = PhaseSkipped
		}
	}

	if !s.Enabled {
		return t.giveBack()
	}

	if paused(r.State) {
		// A pause parks the nozzle higher. Forget the heights, so the
		// parking spot is not taken for a layer.
		t.zs = t.zs[:0]
		t.rising = time.Time{}

		return nil
	}

	if r.HasZ {
		t.zs = append(t.zs, zSample{at: r.At, z: r.Z})
		cut := 0
		for cut < len(t.zs) && r.At.Sub(t.zs[cut].at) > zWindow {
			cut++
		}
		t.zs = t.zs[cut:]
	}

	switch t.phase {
	case PhaseWaiting:
		return t.waitForFirstLayer(r, s)
	case PhaseFirstLayer, PhaseRamping:
		return t.follow(r, s)
	default:
		return nil
	}
}

func (t *tracker) reset() {
	*t = tracker{phase: PhaseIdle}
}

// lowestZ is the lowest height reported within the window.
func (t *tracker) lowestZ() (float64, bool) {
	if len(t.zs) == 0 {
		return 0, false
	}

	low := math.Inf(1)
	for _, s := range t.zs {
		low = math.Min(low, s.z)
	}

	return low, true
}

// waitForFirstLayer starts the assist once the nozzle is low and both the
// nozzle and the bed are at their printing temperatures: the start sequence
// has heated, homed and leveled, and the purge line or the first layer is
// going down.
func (t *tracker) waitForFirstLayer(r Reading, s PrinterSettings) []Action {
	if !r.HasZ || r.Z > firstLayerHeight {
		return nil
	}

	if r.NozzleTarget < printTemperature || r.NozzleTemp < r.NozzleTarget-nearNozzle {
		return nil
	}

	if r.BedTarget <= 0 || r.BedTemp < r.BedTarget-nearBed {
		return nil
	}

	low, _ := t.lowestZ()
	t.phase = PhaseFirstLayer
	t.layer = 1
	t.layerZ = low
	t.settled = r.At.Add(zWindow)

	var actions []Action

	if r.Speed > 0 && s.Speed < r.Speed {
		t.speedFrom, t.speedSet, t.speedLow, t.speedMine = r.Speed, r.Speed, s.Speed, true
		actions = append(actions, t.sendSpeed(s.Speed, r.At))
	}

	if s.BedOffset > 0 {
		from := int(math.Round(r.BedTarget))
		t.bedFrom, t.bedSet, t.bedMine = from, from, true
		actions = append(actions, t.sendBed(min(from+s.BedOffset, maxBed), r.At))
	}

	return actions
}

// follow watches the layers go by and walks the values back.
func (t *tracker) follow(r Reading, s PrinterSettings) []Action {
	actions := t.keep(r)

	low, ok := t.lowestZ()
	if !ok {
		return actions
	}

	// Until the window has filled once, what the start sequence left behind
	// (a lower leveling height) still lowers the first layer's height.
	if t.layer == 1 && r.At.Before(t.settled) {
		t.layerZ = math.Min(t.layerZ, low)

		return actions
	}

	if t.layer == 1 && low < t.layerZ {
		t.layerZ = low
	}

	step := low - t.layerZ
	if step < minLayerStep || step > maxLayerStep {
		t.rising = time.Time{}

		return actions
	}

	// The next layer counts once the lowest height has stood there for a
	// whole window, so a run of travel lifts is not taken for one.
	if t.rising.IsZero() || math.Abs(low-t.risingAt) > minLayerStep {
		t.rising, t.risingAt = r.At, low

		return actions
	}

	if r.At.Sub(t.rising) < zWindow {
		return actions
	}

	t.layer++
	t.layerZ = low
	t.rising = time.Time{}

	return append(actions, t.nextLayer(s, r.At)...)
}

// nextLayer walks the values back by one step, after a layer is done.
func (t *tracker) nextLayer(s PrinterSettings, at time.Time) []Action {
	var actions []Action

	if t.layer == 2 && t.bedMine {
		actions = append(actions, t.sendBed(t.bedFrom, at))
		t.bedMine = false
	}

	ramp := max(s.RampLayers, 1)
	t.rampStep++
	t.phase = PhaseRamping

	if t.speedMine {
		// From the first layer's speed back to the print's own, in equal
		// steps: 60 % over three layers is 73, 86, then 100 %.
		target := t.speedLow + (t.speedFrom-t.speedLow)*min(t.rampStep, ramp)/ramp
		if target != t.speedSet {
			actions = append(actions, t.sendSpeed(target, at))
		}

		if t.rampStep >= ramp {
			t.speedMine = false
		}
	}

	if !t.speedMine && !t.bedMine {
		t.phase = PhaseDone
	}

	return actions
}

// keep notices a value the assist set being changed. Back to the print's own
// value during the first layer is a start sequence setting it again, and the
// assist sets its own once more (a few times at most). Anything else is a
// person in the Tune menu, and the assist leaves that value alone from then on.
func (t *tracker) keep(r Reading) []Action {
	var actions []Action

	lagging := func(sent time.Time) bool { return r.At.Sub(sent) < reportLag }

	switch {
	case !t.speedMine || r.Speed <= 0 || r.Speed == t.speedSet:
	case r.Speed == t.speedWas && lagging(t.speedAt):
		// The printer has not reported the last command yet.
	case r.Speed == t.speedFrom && t.layer == 1 && t.speedAgain < reapplyLimit:
		t.speedAgain++
		actions = append(actions, t.sendSpeed(t.speedSet, r.At))
	default:
		t.speedMine, t.speedTaken = false, true
	}

	bed := int(math.Round(r.BedTarget))

	switch {
	case !t.bedMine || r.BedTarget <= 0 || bed == t.bedSet:
	case bed == t.bedWas && lagging(t.bedAt):
	case bed == t.bedFrom && t.layer == 1 && t.bedAgain < reapplyLimit:
		t.bedAgain++
		actions = append(actions, t.sendBed(t.bedSet, r.At))
	default:
		t.bedMine, t.bedTaken = false, true
	}

	if !t.speedMine && !t.bedMine && (t.phase == PhaseFirstLayer || t.phase == PhaseRamping) {
		t.phase = PhaseDone
	}

	return actions
}

// giveBack puts back what the assist still holds, when it is switched off
// during a print.
func (t *tracker) giveBack() []Action {
	var actions []Action

	if t.speedMine {
		actions = append(actions, setSpeed(t.speedFrom))
	}

	if t.bedMine {
		actions = append(actions, setBed(t.bedFrom))
	}

	t.speedMine, t.bedMine = false, false
	if t.phase == PhaseFirstLayer || t.phase == PhaseRamping {
		t.phase = PhaseDone
	}

	return actions
}
