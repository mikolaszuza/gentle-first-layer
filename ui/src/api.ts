// This module's own routes, and the grant that lets it operate a printer.
import type { Operation } from "@prusa/connect";
import type { ModuleHost } from "@prusa/connect-ui/slots";

/** How the assist treats one printer. */
export interface Settings {
  enabled: boolean;
  /** The first layer's speed, in percent of the print's own. */
  speed: number;
  /** How many °C warmer the bed is during the first layer. */
  bed_offset: number;
  /** Over how many layers after the first the speed climbs back. */
  ramp_layers: number;
}

export type Phase =
  "idle" | "waiting" | "first_layer" | "ramping" | "done" | "skipped";

/** What the assist is doing on one printer. */
export interface Status {
  phase: Phase;
  layer?: number;
  speed?: number;
  bed_raised?: number;
  speed_taken_over?: boolean;
  bed_taken_over?: boolean;
  note?: "unsupported" | "denied" | "refused";
  reason?: string;
}

export interface Printer {
  settings: Settings;
  status: Status;
}

/** The bounds the process holds settings to. */
export const BOUNDS = {
  speed: { min: 20, max: 100 },
  bed_offset: { min: 0, max: 15 },
  ramp_layers: { min: 1, max: 10 },
} as const;

/**
 * The mount of this module's routes. A module is not told its namespace, but
 * the address of a page it would own carries it: `/m/<namespace>/<path>`.
 */
const mountOf = (host: ModuleHost): string => {
  const namespace = host.pageHref("x").split("/")[2] ?? "";

  return `/api/v1/modules/${namespace}`;
};

/**
 * Who the module is in the authorization graph: its instance, which an
 * install names after the manifest's `name` unless the operator chose another.
 * A host that tells the module its own name (`self`, from Prusa Connect
 * 0.1.0-beta.2) is believed instead.
 */
export const subjectOf = (host: ModuleHost): string => {
  const told = (host as { self?: unknown }).self;

  return typeof told === "string" && told !== ""
    ? told
    : "sink:gentle-first-layer";
};

export const listPrinters = async (
  host: ModuleHost,
): Promise<Record<string, Printer>> => {
  const op: Operation = {
    id: "list-printers",
    method: "GET",
    path: `${mountOf(host)}/printers`,
    response: "json",
    anonymous: false,
  };
  const answer = await host.client.transport.call<{
    printers: Record<string, Printer>;
  }>(op, {}, host.signal);

  return answer.printers;
};

export const readPrinter = async (
  host: ModuleHost,
  deviceId: string,
): Promise<Printer> => {
  const op: Operation = {
    id: "read-printer",
    method: "GET",
    path: `${mountOf(host)}/printers/{id}`,
    response: "json",
    anonymous: false,
  };

  return await host.client.transport.call<Printer>(
    op,
    { path: { id: deviceId } },
    host.signal,
  );
};

export const savePrinter = async (
  host: ModuleHost,
  deviceId: string,
  settings: Settings,
): Promise<Printer> => {
  const op: Operation = {
    id: "set-printer",
    method: "PUT",
    path: `${mountOf(host)}/printers/{id}`,
    body: "application/json",
    response: "json",
    anonymous: false,
  };

  return await host.client.transport.call<Printer>(
    op,
    { path: { id: deviceId }, body: settings },
    host.signal,
  );
};

/**
 * Lets the module set this printer's speed and bed temperature, as the
 * person signed in: the operator role on the printer, for the module.
 */
export const allowOperate = async (
  host: ModuleHost,
  deviceId: string,
): Promise<void> => {
  await host.client.writeGrant(
    {
      path: { object_type: "device", object_id: deviceId },
      body: { relation: "operator", subject: subjectOf(host) },
    },
    host.signal,
  );
};

/** Takes that role back. */
export const disallowOperate = async (
  host: ModuleHost,
  deviceId: string,
): Promise<void> => {
  await host.client.withdrawGrant(
    {
      path: { object_type: "device", object_id: deviceId },
      query: { relation: "operator", subject: subjectOf(host) },
    },
    host.signal,
  );
};
