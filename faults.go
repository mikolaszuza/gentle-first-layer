// The codes this module refuses a caller with.
//
// A refusal a person or a client can see is a registered code, never a bare
// English sentence. The code is what the browser half switches on to choose a
// translation, and the English sentence travels beside it so a log line and an
// untranslated screen still read.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// faultDomain is this module's namespace, which the host derives from the Go
// module path in go.mod (github.com/mikolaszuza/gentle-first-layer).
//
//nolint:gochecknoglobals // a domain built once at start-up; a const cannot hold one.
var faultDomain = mustDomain("com.github.aux.mikolaszuza.gentle-first-layer")

//nolint:gochecknoglobals // definitions built once at start-up.
var (
	// RouteNotFound is a path this module does not serve.
	RouteNotFound = fault.Register(faultDomain, "ROUTE_NOT_FOUND", http.StatusNotFound, fault.Document(
		"This module does not serve that path",
		"Read openapi.json. Every path in it is relative to the mount the operator gave this module.",
		fault.RetryNever,
		fault.ShowDetail,
		"path",
	))

	// BadSettings is a printer's settings that cannot be saved as sent.
	BadSettings = fault.Register(faultDomain, "BAD_SETTINGS", http.StatusBadRequest, fault.Document(
		"Those settings cannot be saved",
		"Send {\"enabled\": true, \"speed\": 60, \"bed_offset\": 5, \"ramp_layers\": 3}: the speed is 30 to 100 %, the bed 0 to 15 °C warmer, and the speed climbs back over 1 to 10 layers.",
		fault.RetryNever,
		fault.ShowDetail,
	))

	// NoSuchPrinter is a printer the caller may not see.
	NoSuchPrinter = fault.Register(faultDomain, "NO_SUCH_PRINTER", http.StatusNotFound, fault.Document(
		"There is no such printer",
		"Read the printer's id from the printer list. A printer you may not see answers the same as one that does not exist.",
		fault.RetryNever,
		fault.ShowDetail,
		"printer",
	))

	// SettingsFileFailed is a settings file the process could not read or replace.
	SettingsFileFailed = fault.Register(faultDomain, "SETTINGS_FILE_FAILED", http.StatusInternalServerError, fault.Document(
		"The settings could not be read or saved",
		"Look at the server log for the reason, then at the module's data directory: its permissions and free space.",
		fault.RetryAllowed,
		fault.RedactDetail,
	))

	// FleetUnreadable is a printer list the host did not answer.
	FleetUnreadable = fault.Register(faultDomain, "FLEET_UNREADABLE", http.StatusBadGateway, fault.Document(
		"The printers could not be read",
		"Try again in a moment. If it goes on, look at the server log.",
		fault.RetryAllowed,
		fault.RedactDetail,
	))

	// NowhereToSave is a module installed without the data directory it asks for.
	NowhereToSave = fault.Register(faultDomain, "NOWHERE_TO_SAVE", http.StatusServiceUnavailable, fault.Document(
		"Gentle First Layer has no place to keep its settings",
		"Install the module again and approve the data directory it asks for.",
		fault.RetryNever,
		fault.ShowDetail,
	))
)

func mustDomain(domain string) fault.Domain {
	d, err := fault.NewDomain(domain)
	if err != nil {
		panic(fmt.Sprintf("%s: %s is not a fault domain: %v", name, domain, err))
	}

	return d
}

// refuse writes one refusal as RFC 9457 frames it.
func refuse(w http.ResponseWriter, r *http.Request, raised *fault.Fault) {
	problem := raised.ProblemJSON(r.URL.Path)

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(problem.Status)

	_ = json.NewEncoder(w).Encode(problem)
}
