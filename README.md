# Gentle First Layer

A Prusa Connect Local extension that does for a print's first layer what you would do by hand in the printer's **Tune** menu:

1. When the print reaches its first layer, the **speed drops** (60 % by default) and the **bed goes a few degrees warmer** (+5 °C by default).
2. Once the first layer is down, the bed goes back to the print's own temperature.
3. The speed **climbs back** to 100 % in equal steps over the next few layers (3 by default: 73, 86, 100 %).

It reads only what the printer reports about itself (its state, the nozzle's height, the speed and the temperatures) and sends the same speed and bed temperature commands the Tune menu does. It never reads the G-code, so files from PrusaSlicer, OrcaSlicer or any other slicer get the same help.

## Using it

Open a printer's page in Connect. Under **Gentle first layer** on its Status tab, switch it on and choose:

- the first layer's speed (20–100 %),
- how much warmer the bed is (0–15 °C),
- over how many layers the speed comes back (1–10).

Switching it on gives the extension the operator role on that printer, which is what lets it change the speed and the bed. Switching it off puts back anything it still holds and takes the role back. You need to be allowed to grant that role on the printer (an administrator or the printer's owner).

While it works, the printer's card in the printer list shows a line such as "First layer · 60 % · bed +5 °C".

## What it does when…

- **You change the speed or the bed yourself** in the Tune menu or in Connect: it notices, leaves that value alone for the rest of the print and says so on the printer's page.
- **The G-code sets the bed or speed again** during the first layer (some start sequences do): it sets its own value again, up to three times.
- **The print is paused**: it waits. The parked nozzle is not taken for a new layer.
- **It is switched on mid-print**, or the server restarts mid-print: a print already past its first layer is left alone.
- **The printer doesn't take speed or bed commands from Connect**: the printer's page says so, and nothing is sent.

## How it finds the first layer

A printer reports about every 5 seconds. The first layer starts when the nozzle is within 1 mm of the bed and both the nozzle (above 180 °C) and the bed are at their targets. Prusa start sequences level the bed with the nozzle at 170 °C, so leveling is not taken for printing. A new layer counts once the lowest height reported over 12 seconds has risen by 0.05 to 1 mm and stayed there for 12 seconds, so travel lifts and a parked nozzle are not taken for layers.

The ramp-up therefore starts some seconds after the layer really changed, which is still sooner than walking to the printer.

## Building

`make check` runs the Go tests and the UI's lint, format and type checks. `make release` builds `release/`. See the Makefile for the details. Until the Prusa Connect SDK is published, both halves build against a Prusa Connect checkout in `.core/`.

## License

MIT, see [LICENSE](LICENSE).
