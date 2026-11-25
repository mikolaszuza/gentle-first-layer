// Every printer's settings and status, read once for every card and section
// on the screen and again every few seconds while any of them is drawn.
import type { ModuleHost } from "@prusa/connect-ui/slots";

import { listPrinters, type Printer } from "./api";

/** How often the list is read again while something on screen shows it. */
const EVERY_MS = 3000;

const printers: Record<string, Printer> = $state({});

let watchers = 0;
let timer: ReturnType<typeof setInterval> | undefined;
let reading = false;

const readAll = async (host: ModuleHost): Promise<void> => {
  if (reading) return;
  reading = true;
  try {
    const all = await listPrinters(host);
    for (const [id, printer] of Object.entries(all)) printers[id] = printer;
  } catch {
    // The next tick tries again; what is on screen stays as it was.
  } finally {
    reading = false;
  }
};

/** One printer as last read, or undefined before the first read. Read it while drawing. */
export const printerOf = (deviceId: string): Printer | undefined =>
  printers[deviceId];

/** Puts a printer's fresh answer in place, after a change. */
export const putPrinter = (deviceId: string, printer: Printer): void => {
  printers[deviceId] = printer;
};

/**
 * Keeps the list fresh while the calling component is drawn. Call it from an
 * effect; the function it answers stops it.
 */
export const watchPrinters = (host: ModuleHost): (() => void) => {
  watchers += 1;
  if (watchers === 1) {
    void readAll(host);
    timer = setInterval(() => void readAll(host), EVERY_MS);
  }

  return () => {
    watchers -= 1;
    if (watchers === 0 && timer !== undefined) {
      clearInterval(timer);
      timer = undefined;
    }
  };
};
