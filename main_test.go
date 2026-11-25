package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// fakeFleet is the host's device door with one printer anyone may see,
// except the person "user:stranger", who sees none.
type fakeFleet struct {
	mu      sync.Mutex
	device  deviceapi.Device
	sent    []deviceapi.CommandParams
	deny    bool
	listers []string
}

func (f *fakeFleet) List(_ context.Context, p deviceapi.ListParams) (deviceapi.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.listers = append(f.listers, p.Principal)
	if p.Principal == "user:stranger" {
		return deviceapi.Page{}, nil
	}

	return deviceapi.Page{Items: []deviceapi.Device{f.device}, Total: 1}, nil
}

func (f *fakeFleet) Get(_ context.Context, p deviceapi.GetParams) (deviceapi.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if p.Principal == "user:stranger" || p.ID != f.device.ID {
		return deviceapi.Device{}, deviceapi.ErrNoDevice
	}

	return f.device, nil
}

func (f *fakeFleet) Command(_ context.Context, p deviceapi.CommandParams) (deviceapi.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.deny {
		return deviceapi.Result{}, deviceapi.ErrDenied
	}

	f.sent = append(f.sent, p)

	return deviceapi.Result{OK: true}, nil
}

func (f *fakeFleet) set(key string, value any) {
	f.mu.Lock()
	defer f.mu.Unlock()

	raw, _ := json.Marshal(value)
	f.device.State[key] = raw
}

func newFleet() *fakeFleet {
	f := &fakeFleet{device: deviceapi.Device{
		ID: "printer-1", Kind: "printer", Connected: true,
		Capabilities: []string{verbSetBed, verbSetSpeed},
		State:        map[string]json.RawMessage{},
	}}
	for key, value := range map[string]any{
		"/state": "PRINTING", "/job/id": 42, "/axis/z": 0.2, "/speed": 100,
		"/temperature/bed": 60, "/temperature/bed_target": 60,
		"/tools/0/nozzle_temp": 215, "/tools/0/nozzle_target_temp": 215,
	} {
		f.set(key, value)
	}

	return f
}

func newTestApp(t *testing.T, f fleet) (*app, *time.Time) {
	t.Helper()

	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	a := newApp(slog.New(slog.NewTextHandler(io.Discard, nil)), f, t.Context())
	a.now = func() time.Time { return now }
	a.file = &settingsFile{dir: t.TempDir()}
	a.started = true

	return a, &now
}

// call sends one request to the routes as the host would, with the caller in
// the principal header the SDK reads.
func call(t *testing.T, a *app, method, path, principal, body string) (int, map[string]any) {
	t.Helper()

	r, err := routesapi.Request{Method: method, Path: path, Body: []byte(body), Principal: principal}.HTTP(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)

	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)

	return w.Code, out
}

func TestTheLoopSendsTheTuneMenusCommands(t *testing.T) {
	f := newFleet()
	a, now := newTestApp(t, f)

	if err := a.file.set("printer-1", on()); err != nil {
		t.Fatal(err)
	}

	a.look(t.Context())

	if len(f.sent) != 2 {
		t.Fatalf("sent %+v, want the speed and the bed", f.sent)
	}

	if f.sent[0].Verb != verbSetSpeed || f.sent[0].Args["speed"] != "60" || f.sent[0].Principal != "" {
		t.Fatalf("first command %+v, want 60 %% sent as the module itself", f.sent[0])
	}

	if f.sent[1].Verb != verbSetBed || f.sent[1].Args["bed_temperature"] != "65" {
		t.Fatalf("second command %+v, want the bed at 65", f.sent[1])
	}

	if f.listers[0] != "" {
		t.Fatalf("the loop read the fleet as %q, want the module itself", f.listers[0])
	}

	st := a.statusOf("printer-1")
	if st.Phase != PhaseFirstLayer || st.Speed != 60 {
		t.Fatalf("status %+v", st)
	}

	*now = now.Add(5 * time.Second)
	f.set("/state", "FINISHED")
	a.look(t.Context())

	if st := a.statusOf("printer-1"); st.Phase != PhaseIdle {
		t.Fatalf("status %+v after the print", st)
	}
}

func TestAPrinterTheModuleMayNotOperateSaysSo(t *testing.T) {
	f := newFleet()
	f.deny = true
	a, _ := newTestApp(t, f)

	if err := a.file.set("printer-1", on()); err != nil {
		t.Fatal(err)
	}

	a.look(t.Context())

	if st := a.statusOf("printer-1"); st.Note != NoteDenied {
		t.Fatalf("status %+v, want the denied note", st)
	}
}

func TestAPrinterWithoutTheCommandsIsUnsupported(t *testing.T) {
	f := newFleet()
	f.device.Capabilities = []string{verbSetBed}
	a, _ := newTestApp(t, f)

	if err := a.file.set("printer-1", on()); err != nil {
		t.Fatal(err)
	}

	a.look(t.Context())

	if len(f.sent) != 0 || a.statusOf("printer-1").Note != NoteUnsupported {
		t.Fatalf("sent %+v, status %+v", f.sent, a.statusOf("printer-1"))
	}
}

func TestSettingsAreSavedAndRead(t *testing.T) {
	a, _ := newTestApp(t, newFleet())

	code, body := call(t, a, http.MethodGet, "/printers/printer-1", "user:alice", "")
	if code != http.StatusOK {
		t.Fatalf("read: %d %v", code, body)
	}

	settings := body["settings"].(map[string]any)
	if settings["enabled"] != false || settings["speed"] != float64(60) {
		t.Fatalf("a new printer reads %v, want switched off with the defaults", settings)
	}

	code, body = call(t, a, http.MethodPut, "/printers/printer-1", "user:alice",
		`{"enabled": true, "speed": 50, "bed_offset": 3, "ramp_layers": 2}`)
	if code != http.StatusOK {
		t.Fatalf("change: %d %v", code, body)
	}

	_, body = call(t, a, http.MethodGet, "/printers", "user:alice", "")
	printers := body["printers"].(map[string]any)
	got := printers["printer-1"].(map[string]any)["settings"].(map[string]any)

	if got["enabled"] != true || got["speed"] != float64(50) || got["ramp_layers"] != float64(2) {
		t.Fatalf("after the change the list reads %v", got)
	}
}

func TestSettingsOutOfBoundsAreRefused(t *testing.T) {
	a, _ := newTestApp(t, newFleet())

	for _, body := range []string{
		`{"enabled": true, "speed": 10, "bed_offset": 5, "ramp_layers": 3}`,
		`{"enabled": true, "speed": 60, "bed_offset": 30, "ramp_layers": 3}`,
		`{"enabled": true, "speed": 60, "bed_offset": 5, "ramp_layers": 0}`,
		`{"enabled": true, "speed": 60, "bed_offset": 5, "ramp_layers": 3, "nozzle": 9}`,
		`not json`,
	} {
		code, answer := call(t, a, http.MethodPut, "/printers/printer-1", "user:alice", body)
		if code != http.StatusBadRequest {
			t.Errorf("%s: %d %v, want 400", body, code, answer)
		}
	}
}

func TestAPrinterYouMayNotSeeDoesNotExist(t *testing.T) {
	a, _ := newTestApp(t, newFleet())

	code, _ := call(t, a, http.MethodPut, "/printers/printer-1", "user:stranger",
		`{"enabled": true, "speed": 60, "bed_offset": 5, "ramp_layers": 3}`)
	if code != http.StatusNotFound {
		t.Fatalf("a stranger's change answered %d, want 404", code)
	}

	_, body := call(t, a, http.MethodGet, "/printers", "user:stranger", "")
	if printers := body["printers"].(map[string]any); len(printers) != 0 {
		t.Fatalf("a stranger's list holds %v", printers)
	}
}

func TestTheOpenAPIDocumentIsOneTheHostTakes(t *testing.T) {
	a, _ := newTestApp(t, newFleet())

	if _, err := a.methods()[wirerpc.MethodInfo](t.Context(), wirerpc.Call{}); err != nil {
		t.Fatalf("the host would refuse the module's description: %v", err)
	}
}

func TestAnOfflinePrinterIsNotUnsupported(t *testing.T) {
	f := newFleet()
	f.device.Connected = false
	f.device.Capabilities = nil
	a, _ := newTestApp(t, f)

	a.look(t.Context())

	if st := a.statusOf("printer-1"); st.Note != "" || st.Phase != PhaseIdle {
		t.Fatalf("an offline printer reads %+v", st)
	}
}
