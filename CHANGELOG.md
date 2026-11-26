# Changelog

## 0.1.3

- Changing a printer's settings now takes the operator role on that printer, not only seeing it. Someone who may only look at a printer can no longer change the speed or the bed the assist applies with the role someone else gave it. The module asks the host with `access.check` (operate on the device) before it saves, and refuses with `NOT_AN_OPERATOR` (403).
- Switching the assist off through the API: a module has no way to withdraw a grant itself, so the API description now says a client should withdraw the operator grant as the page does. A grant left behind does nothing while the assist is off, and switching it back on takes the operator role.

## 0.1.2

- The note under a speed below 50 % is gone. The slider still turns yellow there.

## 0.1.1

- The first layer's speed goes down to 30 %. Below 50 %, which is as low as the printer's screen goes, the slider turns yellow and says it is rarely needed.

## 0.1.0

- Slows the first layer down and warms the bed a few degrees, then puts the bed back and brings the speed back up over the next layers, as you would in the printer's Tune menu.
- Switched on per printer on its page, with the speed, the bed offset and the number of layers to ramp over.
- Steps back when you change the speed or the bed yourself.
- Works from what the printer reports, so G-code from any slicer gets the same help.
