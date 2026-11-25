<!--
  One line on a printer's card in the listing while the assist holds its
  first layer or brings the speed back, nothing otherwise.
-->
<script lang="ts">
  import { heldHost } from './host';
  import { printerOf, watchPrinters } from './printers.svelte';

  let { deviceId }: { deviceId: string } = $props();

  const host = heldHost();

  $effect(() => watchPrinters(host));

  const status = $derived(printerOf(deviceId)?.status);
</script>

{#if status?.phase === 'first_layer'}
  <span class="truncate text-xs text-muted-foreground tabular-nums">
    {host.t('gentle-first-layer.line.first_layer', {
      speed: status.speed ?? 100,
      bed: status.bed_raised ?? 0,
    })}
  </span>
{:else if status?.phase === 'ramping'}
  <span class="truncate text-xs text-muted-foreground tabular-nums">
    {host.t('gentle-first-layer.line.ramping', { speed: status.speed ?? 100 })}
  </span>
{/if}
