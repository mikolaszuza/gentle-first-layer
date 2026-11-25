// Command gentle-first-layer does for a print's first layer what a person does
// by hand in the printer's Tune menu: it slows the print down and warms the
// bed a little while the first layer goes down, then puts the bed back and
// brings the speed back up over the next few layers.
//
// It works from what the printer reports (its state, the nozzle's height, the
// speed and the temperatures) and changes the printer with the same speed and
// bed temperature commands the Tune menu sends. It never reads the G-code, so
// any slicer's file gets the same help.
//
// The process speaks the plugin wire protocol over its own stdin and stdout.
// The wire setup follows Prusa's extension template,
// prusa3d/prusa-connect-extension-example (MIT).
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/extension"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wire"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

const (
	name    = "gentle-first-layer"
	release = extension.Version("0.1.2")
)

//go:embed openapi.json
var openAPI []byte

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	line, err := wire.Handshake{
		Protocol: wire.ProtocolMajor,
		MinMinor: wire.MinMinor,
		MaxMinor: wire.MaxMinor,
		Address:  wire.StdioAddress,
	}.Line()
	if err != nil {
		return fmt.Errorf("render the handshake: %w", err)
	}

	if _, err = os.Stdout.WriteString(line); err != nil {
		return fmt.Errorf("write the handshake: %w", err)
	}

	// The routes and the loop call back into the host, so the client needs the
	// session first. The map is filled below, before Serve reads it.
	serves := wirerpc.Methods{}

	conn, err := wirerpc.NewConn(stdio{}, wirerpc.Options{
		Role:    wirerpc.RoleChild,
		Methods: serves,
	})
	if err != nil {
		return fmt.Errorf("build the session: %w", err)
	}

	a := newApp(slog.New(slog.NewTextHandler(os.Stderr, nil)), deviceapi.NewClient(conn), ctx)
	maps.Copy(serves, a.methods())

	fmt.Fprintf(os.Stderr, "%s %s is serving on stdio as pid %d\n", name, release, os.Getpid())

	if err = conn.Serve(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	return nil
}

// app holds what the routes and the loop share.
type app struct {
	log   *slog.Logger
	fleet fleet
	now   func() time.Time
	// base outlives every call: the loop runs on it.
	base context.Context //nolint:containedctx // the process's own lifetime, set once.

	mu       sync.Mutex
	file     *settingsFile
	started  bool
	trackers map[string]*tracker
	notes    map[string]PrinterStatus
}

// fleet is the part of the host's device door the module uses.
type fleet interface {
	List(ctx context.Context, params deviceapi.ListParams) (deviceapi.Page, error)
	Get(ctx context.Context, params deviceapi.GetParams) (deviceapi.Device, error)
	Command(ctx context.Context, params deviceapi.CommandParams) (deviceapi.Result, error)
}

func newApp(log *slog.Logger, f fleet, base context.Context) *app { //nolint:revive // base is the process's lifetime.
	return &app{
		log:      log,
		fleet:    f,
		now:      time.Now,
		base:     base,
		trackers: map[string]*tracker{},
		notes:    map[string]PrinterStatus{},
	}
}

func (a *app) methods() wirerpc.Methods {
	return wirerpc.Methods{
		wirerpc.MethodInfo: func(context.Context, wirerpc.Call) (any, error) {
			return routesapi.Describe(
				wirerpc.Info{
					Name:     name,
					Version:  release,
					Contract: wirerpc.Contract,
					Serves:   []wirerpc.Method{wirerpc.MethodConfigure},
				}, openAPI)
		},
		wirerpc.MethodConfigure: wirerpc.ConfigureGranted(a.configure),
		wirerpc.MethodShutdown: func(context.Context, wirerpc.Call) (any, error) {
			fmt.Fprintf(os.Stderr, "%s was asked to stop\n", name)

			return nil, nil
		},
		routesapi.MethodServe: routesapi.Serve(a.routes()),
	}
}

// configure takes the data directory and starts the loop the first time one
// is granted. Without one the module keeps no settings, so every printer is
// switched off and the routes say why.
func (a *app) configure(_ context.Context, _ struct{}, grants wirerpc.Grants) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if grants.Data == "" || a.started {
		return nil
	}

	a.file = &settingsFile{dir: grants.Data}
	a.started = true

	go a.loop(a.base)

	return nil
}

func (a *app) settingsFile() *settingsFile {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.file
}

// loop looks at the fleet every few seconds until the process stops.
func (a *app) loop(ctx context.Context) {
	tick := time.NewTicker(lookEvery)
	defer tick.Stop()

	for {
		a.look(ctx)

		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }
