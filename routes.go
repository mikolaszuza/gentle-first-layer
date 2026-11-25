// The routes the page calls. The host decides who may call them before a
// request reaches this process: a GET needs view_resource on the instance, a
// PUT needs operate_resource. Each route then reads the printer as the person
// calling, so a person sees and changes only the printers they may see.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/fault"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
)

// Printer is one printer's settings and what the assist is doing on it.
type Printer struct {
	Settings PrinterSettings `json:"settings"`
	Status   PrinterStatus   `json:"status"`
}

// maxSettingsBytes is the most one change of settings may weigh.
const maxSettingsBytes = 4 << 10

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()

	// Every printer the caller may see, for the line on each printer's card.
	mux.HandleFunc("GET /printers", a.withFile(func(w http.ResponseWriter, r *http.Request, f *settingsFile) {
		devices, err := a.listAll(r.Context(), routesapi.PrincipalOf(r))
		if err != nil {
			a.fleetFailed(w, r, err)

			return
		}

		all, err := f.all()
		if err != nil {
			a.fileFailed(w, r, err)

			return
		}

		out := make(map[string]Printer, len(devices))
		for _, d := range devices {
			s, ok := all[d.ID]
			if !ok {
				s = Defaults()
			}

			out[d.ID] = Printer{Settings: s, Status: a.statusOf(d.ID)}
		}

		answer(w, http.StatusOK, map[string]any{"printers": out})
	}))

	mux.HandleFunc("GET /printers/{id}", a.withPrinter(func(w http.ResponseWriter, r *http.Request, f *settingsFile, id string) {
		s, err := f.of(id)
		if err != nil {
			a.fileFailed(w, r, err)

			return
		}

		answer(w, http.StatusOK, Printer{Settings: s, Status: a.statusOf(id)})
	}))

	mux.HandleFunc("PUT /printers/{id}", a.withPrinter(func(w http.ResponseWriter, r *http.Request, f *settingsFile, id string) {
		var s PrinterSettings

		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingsBytes))
		decoder.DisallowUnknownFields()

		if err := decoder.Decode(&s); err != nil {
			refuse(w, r, BadSettings.New(fmt.Sprintf("the body is not a printer's settings: %v", err)))

			return
		}

		if err := s.validate(); err != nil {
			refuse(w, r, BadSettings.New(err.Error()))

			return
		}

		if err := f.set(id, s); err != nil {
			if errors.Is(err, errInvalid) {
				refuse(w, r, BadSettings.New(err.Error()))
			} else {
				a.fileFailed(w, r, err)
			}

			return
		}

		a.log.InfoContext(r.Context(), "a printer's settings changed", "printer", id,
			"by", routesapi.PrincipalOf(r), "enabled", s.Enabled, "speed", s.Speed,
			"bed_offset", s.BedOffset, "ramp_layers", s.RampLayers)

		answer(w, http.StatusOK, Printer{Settings: s, Status: a.statusOf(id)})
	}))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		refuse(w, r, RouteNotFound.
			New("this module serves no route at "+r.URL.Path).
			With(fault.StringParam("path", r.URL.Path)))
	})

	return mux
}

// withFile runs handle with the settings file, or refuses when the module was
// installed without a data directory to keep it in.
func (a *app) withFile(handle func(http.ResponseWriter, *http.Request, *settingsFile)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := a.settingsFile()
		if f == nil {
			refuse(w, r, NowhereToSave.New("no data directory was granted to "+name))

			return
		}

		handle(w, r, f)
	}
}

// withPrinter also reads the printer as the caller, so a printer they may not
// see is one that does not exist.
func (a *app) withPrinter(handle func(http.ResponseWriter, *http.Request, *settingsFile, string)) http.HandlerFunc {
	return a.withFile(func(w http.ResponseWriter, r *http.Request, f *settingsFile) {
		id := r.PathValue("id")

		_, err := a.fleet.Get(r.Context(), deviceapi.GetParams{Principal: routesapi.PrincipalOf(r), ID: id})
		switch {
		case errors.Is(err, deviceapi.ErrNoDevice) || errors.Is(err, deviceapi.ErrDenied):
			refuse(w, r, NoSuchPrinter.New("no printer "+id+" that you may see").With(fault.StringParam("printer", id)))
		case err != nil:
			a.fleetFailed(w, r, err)
		default:
			handle(w, r, f, id)
		}
	})
}

func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(body)
}

// fileFailed and fleetFailed refuse a request the module could not serve. The
// reason goes to the log, not to the answer, which any viewer can read.
func (a *app) fileFailed(w http.ResponseWriter, r *http.Request, err error) {
	fmt.Fprintf(os.Stderr, "%s: %s %s: %v\n", name, r.Method, r.URL.Path, err)
	refuse(w, r, SettingsFileFailed.New(err.Error()))
}

func (a *app) fleetFailed(w http.ResponseWriter, r *http.Request, err error) {
	fmt.Fprintf(os.Stderr, "%s: %s %s: %v\n", name, r.Method, r.URL.Path, err)
	refuse(w, r, FleetUnreadable.New(err.Error()))
}
