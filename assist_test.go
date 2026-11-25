package main

import (
	"slices"
	"testing"
	"time"
)

// printer is a simulated printer reporting every 5 seconds, as a Buddy
// printer does while it prints.
type printer struct {
	t       *testing.T
	at      time.Time
	r       Reading
	tracker tracker
	sent    []Action
	// applies says whether the printer takes the assist's commands; a real
	// one reports the new value with its next report.
	applies bool
}

func newPrinter(t *testing.T) *printer {
	t.Helper()

	return &printer{
		t:       t,
		at:      time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC),
		r:       Reading{State: "IDLE", Speed: 100, BedTemp: 25, NozzleTemp: 25, HasZ: true},
		tracker: tracker{phase: PhaseIdle},
		applies: true,
	}
}

// report is one telemetry report: the printer's values, after waiting the
// given time.
func (p *printer) report(after time.Duration, s PrinterSettings) []Action {
	p.at = p.at.Add(after)
	p.r.At = p.at
	actions := p.tracker.step(p.r, s)

	p.sent = append(p.sent, actions...)
	if p.applies {
		for _, a := range actions {
			switch a.Verb {
			case verbSetSpeed:
				p.r.Speed = atoi(p.t, a.Args["speed"])
			case verbSetBed:
				p.r.BedTarget = float64(atoi(p.t, a.Args["bed_temperature"]))
			}
		}
	}

	return actions
}

func atoi(t *testing.T, s string) int {
	t.Helper()

	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("%q is not a number", s)
		}
		n = n*10 + int(c-'0')
	}

	return n
}

// startSequence runs a Prusa start sequence: heat the bed, home, level with
// the nozzle at 170 °C, heat the nozzle, and go down to the first layer.
func (p *printer) startSequence(s PrinterSettings, bed, nozzle float64) {
	p.r.State, p.r.Job = "PRINTING", "42"
	p.r.BedTarget, p.r.NozzleTarget = bed, 170
	p.r.Z = 20

	for range 6 { // heating
		p.report(5*time.Second, s)
	}

	p.r.BedTemp, p.r.NozzleTemp = bed, 170

	for _, z := range []float64{20, 5, 0.4, 2, 0.35, 2.1, 0.4, 2} { // leveling
		p.r.Z = z
		p.report(5*time.Second, s)
	}

	p.r.NozzleTarget = nozzle

	for range 4 { // heating the nozzle up high above the bed
		p.r.Z = 10
		p.report(5*time.Second, s)
	}

	p.r.NozzleTemp = nozzle
}

// layer prints one layer at height z for the given time, with a travel lift
// of 0.6 mm in some of the reports.
func (p *printer) layer(s PrinterSettings, z float64, d time.Duration) {
	for i := range int(d / (5 * time.Second)) {
		p.r.Z = z
		if i%3 == 1 {
			p.r.Z = z + 0.6
		}
		p.report(5*time.Second, s)
	}
}

func on() PrinterSettings {
	s := Defaults()
	s.Enabled = true

	return s
}

func verbs(actions []Action) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		v := a.Args["speed"]
		if a.Verb == verbSetBed {
			v = "bed " + a.Args["bed_temperature"]
		}
		out = append(out, v)
	}

	return out
}

func TestAWholePrintSlowsTheFirstLayerAndWalksBack(t *testing.T) {
	p := newPrinter(t)
	s := on()

	p.startSequence(s, 60, 215)

	if len(p.sent) != 0 {
		t.Fatalf("the assist acted during the start sequence: %v", verbs(p.sent))
	}

	if p.tracker.phase != PhaseWaiting {
		t.Fatalf("phase %s before the first layer, want waiting", p.tracker.phase)
	}

	p.layer(s, 0.2, 3*time.Minute)

	if got := verbs(p.sent); !slices.Equal(got, []string{"60", "bed 65"}) {
		t.Fatalf("during the first layer the assist sent %v, want 60 %% and the bed at 65", got)
	}

	if st := p.tracker.status(); st.Phase != PhaseFirstLayer || st.Speed != 60 || st.BedRaised != 5 || st.Layer != 1 {
		t.Fatalf("status during the first layer: %+v", st)
	}

	for i, z := range []float64{0.4, 0.6, 0.8, 1.0} {
		p.layer(s, z, time.Minute)
		t.Logf("after layer %d: %v", i+2, verbs(p.sent))
	}

	if got := verbs(p.sent); !slices.Equal(got, []string{"60", "bed 65", "bed 60", "73", "86", "100"}) {
		t.Fatalf("over the print the assist sent %v", got)
	}

	if p.tracker.phase != PhaseDone {
		t.Fatalf("phase %s after the ramp, want done", p.tracker.phase)
	}

	p.r.State = "FINISHED"
	p.report(5*time.Second, s)

	if p.tracker.phase != PhaseIdle {
		t.Fatalf("phase %s after the print, want idle", p.tracker.phase)
	}
}

func TestTravelLiftsAreNotLayers(t *testing.T) {
	p := newPrinter(t)
	s := on()
	p.startSequence(s, 60, 215)

	// A first layer with a lift in every other report, for ten minutes.
	for i := range 120 {
		p.r.Z = 0.2
		if i%2 == 1 {
			p.r.Z = 0.8
		}
		p.report(5*time.Second, s)
	}

	if p.tracker.layer != 1 {
		t.Fatalf("layer %d after a long first layer full of lifts, want 1", p.tracker.layer)
	}
}

func TestAPersonInTheTuneMenuWins(t *testing.T) {
	p := newPrinter(t)
	s := on()
	p.startSequence(s, 60, 215)
	p.layer(s, 0.2, time.Minute)

	// The person sets 80 % themselves during the first layer.
	p.r.Speed = 80
	p.layer(s, 0.2, time.Minute)

	for _, z := range []float64{0.4, 0.6, 0.8, 1.0} {
		p.layer(s, z, time.Minute)
	}

	for _, a := range p.sent[2:] {
		if a.Verb == verbSetSpeed {
			t.Fatalf("the assist set the speed to %s after the person changed it", a.Args["speed"])
		}
	}

	st := p.tracker.status()
	if !st.SpeedTakenOver || st.BedTakenOver {
		t.Fatalf("status %+v, want the speed taken over and the bed not", st)
	}

	if p.r.Speed != 80 {
		t.Fatalf("speed %d at the end, want the person's 80", p.r.Speed)
	}
}

func TestTheReportBeforeACommandLandsIsNotATakeover(t *testing.T) {
	p := newPrinter(t)
	s := on()
	p.startSequence(s, 60, 215)

	// The printer reports its old values for one more report after each
	// command.
	p.applies = false
	p.r.Z = 0.2
	actions := p.report(5*time.Second, s)
	p.report(5*time.Second, s)

	for _, a := range actions {
		if a.Verb == verbSetSpeed {
			p.r.Speed = 60
		} else {
			p.r.BedTarget = 65
		}
	}

	p.layer(s, 0.2, time.Minute)

	if st := p.tracker.status(); st.SpeedTakenOver || st.BedTakenOver || st.Speed != 60 {
		t.Fatalf("status %+v: a late report was taken for a person", st)
	}
}

func TestAStartSequenceThatSetsTheBedAgainIsFollowed(t *testing.T) {
	p := newPrinter(t)
	s := on()
	p.startSequence(s, 60, 215)
	p.layer(s, 0.2, 20*time.Second)

	// The G-code sets the first layer's bed temperature once more.
	p.r.BedTarget = 60
	p.layer(s, 0.2, 30*time.Second)

	if p.r.BedTarget != 65 {
		t.Fatalf("bed target %.0f, want the assist's 65 set again", p.r.BedTarget)
	}

	if p.tracker.status().BedTakenOver {
		t.Fatal("a start sequence setting the bed again was taken for a person")
	}
}

func TestSwitchingOffMidPrintPutsBackWhatItChanged(t *testing.T) {
	p := newPrinter(t)
	s := on()
	p.startSequence(s, 85, 250)
	p.layer(s, 0.2, time.Minute)

	off := s
	off.Enabled = false
	p.report(5*time.Second, off)

	if p.r.Speed != 100 || p.r.BedTarget != 85 {
		t.Fatalf("speed %d and bed %.0f after switching off, want 100 and 85", p.r.Speed, p.r.BedTarget)
	}

	p.layer(off, 0.4, time.Minute)

	if got := verbs(p.sent); !slices.Equal(got, []string{"60", "bed 90", "100", "bed 85"}) {
		t.Fatalf("sent %v", got)
	}
}

func TestAPrintSeenLateIsLeftAlone(t *testing.T) {
	p := newPrinter(t)
	s := on()
	p.r.State, p.r.Job = "PRINTING", "7"
	p.r.BedTarget, p.r.BedTemp, p.r.NozzleTarget, p.r.NozzleTemp = 60, 60, 215, 215
	p.layer(s, 12.4, time.Minute)

	if len(p.sent) != 0 || p.tracker.phase != PhaseSkipped {
		t.Fatalf("a print at 12 mm got %v, phase %s", verbs(p.sent), p.tracker.phase)
	}
}

func TestAPauseIsNotALayer(t *testing.T) {
	p := newPrinter(t)
	s := on()
	p.startSequence(s, 60, 215)
	p.layer(s, 0.2, time.Minute)

	p.r.State = "PAUSED"
	for range 12 {
		p.r.Z = 0.5 // parked a little higher
		p.report(5*time.Second, s)
	}

	p.r.State = "PRINTING"
	p.layer(s, 0.2, time.Minute)

	if p.tracker.layer != 1 {
		t.Fatalf("layer %d after a pause on the first layer, want 1", p.tracker.layer)
	}
}

func TestASwitchedOffPrinterGetsNothing(t *testing.T) {
	p := newPrinter(t)
	s := Defaults()
	p.startSequence(s, 60, 215)
	p.layer(s, 0.2, time.Minute)
	p.layer(s, 0.4, time.Minute)

	if len(p.sent) != 0 {
		t.Fatalf("a switched-off printer got %v", verbs(p.sent))
	}
}

func TestTheBedNeverGoesPastTheCommandsLimit(t *testing.T) {
	p := newPrinter(t)
	s := on()
	s.BedOffset = 15
	p.startSequence(s, 110, 285)
	p.layer(s, 0.2, 30*time.Second)

	if p.r.BedTarget != maxBed {
		t.Fatalf("bed target %.0f, want it held at %d", p.r.BedTarget, maxBed)
	}
}
