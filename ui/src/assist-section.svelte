<!--
  The section on a printer's Status tab: the switch, how gentle the first
  layer is, and what the assist is doing on this printer right now.
-->
<script lang="ts">
  import {
    Button,
    Label,
    SliderField,
    Switch,
  } from '@prusa/connect-ui/primitives';

  import {
    allowOperate,
    BOUNDS,
    disallowOperate,
    readPrinter,
    savePrinter,
    type Settings,
    type Status,
  } from './api';
  import { heldHost } from './host';
  import { printerOf, putPrinter, watchPrinters } from './printers.svelte';

  let { deviceId }: { deviceId: string } = $props();

  const host = heldHost();
  const t = host.t;

  $effect(() => watchPrinters(host));

  $effect(() => {
    const id = deviceId;
    readPrinter(host, id)
      .then((printer) => putPrinter(id, printer))
      .catch(() => {
        loadFailed = true;
      });
  });

  const printer = $derived(printerOf(deviceId));
  const settings = $derived(printer?.settings);
  const status = $derived(printer?.status);

  let busy = $state(false);
  let loadFailed = $state(false);
  let problem: string | undefined = $state();

  const save = async (next: Settings): Promise<void> => {
    busy = true;
    problem = undefined;
    try {
      putPrinter(deviceId, await savePrinter(host, deviceId, next));
    } catch {
      problem = t('gentle-first-layer.error.save');
    } finally {
      busy = false;
    }
  };

  /**
   * On: first the module may operate this printer, then the settings say so.
   * Off: the settings first, so the process puts back what it changed while
   * it still may, then the permission goes.
   */
  const switchTo = async (on: boolean): Promise<void> => {
    if (!settings) return;
    busy = true;
    problem = undefined;
    try {
      if (on) {
        try {
          await allowOperate(host, deviceId);
        } catch {
          problem = t('gentle-first-layer.error.grant');

          return;
        }
        putPrinter(
          deviceId,
          await savePrinter(host, deviceId, { ...settings, enabled: true }),
        );
      } else {
        putPrinter(
          deviceId,
          await savePrinter(host, deviceId, { ...settings, enabled: false }),
        );
        // The process gives back what it holds on its next look.
        await new Promise((resolve) => setTimeout(resolve, 2500));
        await disallowOperate(host, deviceId).catch(() => undefined);
      }
    } catch {
      problem = t('gentle-first-layer.error.save');
    } finally {
      busy = false;
    }
  };

  const regrant = async (): Promise<void> => {
    busy = true;
    problem = undefined;
    try {
      await allowOperate(host, deviceId);
    } catch {
      problem = t('gentle-first-layer.error.grant');
    } finally {
      busy = false;
    }
  };

  const change = (key: keyof Omit<Settings, 'enabled'>, value: number) => {
    if (!settings || settings[key] === value) return;
    void save({ ...settings, [key]: value });
  };

  /** One sentence on what the assist is doing now. */
  const phaseLine = (s: Status): string => {
    switch (s.phase) {
      case 'waiting':
        return t('gentle-first-layer.phase.waiting');
      case 'first_layer':
        return t('gentle-first-layer.phase.first_layer', {
          speed: s.speed ?? 100,
          bed: s.bed_raised ?? 0,
        });
      case 'ramping':
        return t('gentle-first-layer.phase.ramping', {
          layer: s.layer ?? 2,
          speed: s.speed ?? 100,
        });
      case 'done':
        return t('gentle-first-layer.phase.done');
      case 'skipped':
        return t('gentle-first-layer.phase.skipped');
      default:
        return t('gentle-first-layer.phase.idle');
    }
  };

  const takenOver = $derived.by(() => {
    if (status?.speed_taken_over && status.bed_taken_over)
      return t('gentle-first-layer.taken.both');
    if (status?.speed_taken_over) return t('gentle-first-layer.taken.speed');
    if (status?.bed_taken_over) return t('gentle-first-layer.taken.bed');

    return undefined;
  });
</script>

<div class="flex flex-col gap-4 text-sm">
  {#if !settings}
    <p class="text-muted-foreground">
      {loadFailed
        ? t('gentle-first-layer.error.load')
        : t('gentle-first-layer.loading')}
    </p>
  {:else if status?.note === 'unsupported'}
    <p class="text-muted-foreground">
      {t('gentle-first-layer.note.unsupported')}
    </p>
  {:else}
    <div class="flex items-start gap-3">
      <Switch
        id="gentle-first-layer-{deviceId}"
        checked={settings.enabled}
        disabled={busy}
        onCheckedChange={(on: boolean) => void switchTo(on)}
        aria-label={t('gentle-first-layer.switch')}
      />
      <span class="flex flex-col gap-0.5">
        <Label for="gentle-first-layer-{deviceId}">
          {t('gentle-first-layer.switch')}
        </Label>
        <span class="text-xs text-muted-foreground">
          {t('gentle-first-layer.what', {
            speed: settings.speed,
            bed: settings.bed_offset,
            layers: settings.ramp_layers,
          })}
        </span>
      </span>
    </div>

    {#if settings.enabled}
      <div class="grid gap-3 sm:grid-cols-3">
        <div class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">
            {t('gentle-first-layer.speed')}
          </span>
          <SliderField
            value={settings.speed}
            min={BOUNDS.speed.min}
            max={BOUNDS.speed.max}
            step={5}
            unit="%"
            label={t('gentle-first-layer.speed')}
            disabled={busy}
            onCommit={(v: number) => change('speed', v)}
          />
        </div>
        <div class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">
            {t('gentle-first-layer.bed')}
          </span>
          <SliderField
            value={settings.bed_offset}
            min={BOUNDS.bed_offset.min}
            max={BOUNDS.bed_offset.max}
            unit="°C"
            label={t('gentle-first-layer.bed')}
            disabled={busy}
            onCommit={(v: number) => change('bed_offset', v)}
          />
        </div>
        <div class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">
            {t('gentle-first-layer.ramp')}
          </span>
          <SliderField
            value={settings.ramp_layers}
            min={BOUNDS.ramp_layers.min}
            max={BOUNDS.ramp_layers.max}
            unit={t('gentle-first-layer.layers')}
            label={t('gentle-first-layer.ramp')}
            disabled={busy}
            onCommit={(v: number) => change('ramp_layers', v)}
          />
        </div>
      </div>

      {#if status}
        <p aria-live="polite">{phaseLine(status)}</p>
      {/if}
      {#if takenOver}
        <p class="text-xs text-muted-foreground">{takenOver}</p>
      {/if}
      {#if status?.note === 'denied'}
        <div class="flex flex-wrap items-center gap-3">
          <p class="text-xs text-warning">
            {t('gentle-first-layer.note.denied')}
          </p>
          <Button
            size="sm"
            variant="secondary"
            disabled={busy}
            onclick={() => void regrant()}
          >
            {t('gentle-first-layer.regrant')}
          </Button>
        </div>
      {:else if status?.note === 'refused'}
        <p class="text-xs text-warning">
          {t('gentle-first-layer.note.refused', {
            reason: status.reason ?? '',
          })}
        </p>
      {/if}
    {/if}
  {/if}

  {#if problem}
    <p class="text-xs text-destructive">{problem}</p>
  {/if}
</div>
