<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { helpRequest } from '../help';
  import type { Actor, PublicEndpointView } from '../types';
  import '../../styles/ticket-connections.css';
  import ConfirmDialog from './ConfirmDialog.svelte';
  import EmailInboxes from './EmailInboxes.svelte';
  import HelpDrawer from './HelpDrawer.svelte';
  import PublicAccessPanel from './PublicAccessPanel.svelte';
  import TicketWebhookGuide from './tickets/TicketWebhookGuide.svelte';
  import TicketWebhooks from './tickets/TicketWebhooks.svelte';
  import { connectionTabs, containTab, defaultConnectionTab, nextConnectionTab, publicAccessStatus, type ConnectionTab } from './tickets/connections';

  // Connect apps: an on-demand manager over the ticket queue. One area at a
  // time (Email, Webhooks, Public access); every panel stays mounted while
  // open so drafts and a one-time webhook URL survive tab changes, and
  // closing with either asks first. See docs/TC337_TICKETS_UX_FAILURES.md.
  export let user: Actor;
  export let onPublicAccessChanged: () => void = () => undefined;
  /** Public access state from the opener, for the tab summary (additive). */
  export let publicAccess: PublicEndpointView | null = null;
  /** Selected area; bind to remember it while the page is open (additive). */
  export let tab: ConnectionTab = defaultConnectionTab(user.admin, publicAccess);
  /** Called once closing is confirmed (additive). */
  export let onClose: () => void = () => undefined;
  /** Unsaved drafts or an unacknowledged webhook URL (additive, bindable). */
  export let dirty = false;

  type CloseRequest = { title: string; message: string; confirm: string; cancel: string };

  let dialog: HTMLDivElement;
  let publicPanel: HTMLElement | null = null;
  let closeButton: HTMLButtonElement;
  let webhooks: TicketWebhooks | null = null;
  // Bumped when guided setup may have created the first inbox.
  let inboxesKey = 0;
  let emailLoaded = false;
  let emailOn = false;
  let inboxCount = 0;
  let emailDirty = false;
  let publicDirty = false;
  let webhookCount = 0;
  let webhookDirty = false;
  let secretPending = false;
  let guideOpen = false;
  let closing: CloseRequest | null = null;
  let closeReturn: HTMLElement | null = null;
  let helpOpen = false;
  let helpOpener: HTMLElement | null = null;

  $: dirty = Boolean(emailDirty || webhookDirty || publicDirty || secretPending);
  $: publicStatus = publicAccessStatus(publicAccess);
  $: tabMeta = {
    email: !emailLoaded ? { label: '', tone: 'count' } : emailOn ? { label: String(inboxCount), tone: 'count' } : { label: 'Off', tone: 'off' },
    webhooks: secretPending ? { label: 'Copy URL', tone: 'warn' } : { label: String(webhookCount), tone: 'count' },
    public: publicStatus
  } as Record<ConnectionTab, { label: string; tone: string }>;

  const unsubscribeHelp = helpRequest.subscribe((value) => {
    if (value && !helpOpen) helpOpener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (!value && helpOpen) {
      const opener = helpOpener;
      void tick().then(() => { if (opener?.isConnected && dialog?.contains(opener)) opener.focus(); });
    }
    helpOpen = Boolean(value);
  });

  async function select(next: ConnectionTab, focus = false) {
    tab = next;
    if (!focus) return;
    await tick();
    dialog?.querySelector<HTMLElement>(`#connections-tab-${next}`)?.focus();
  }

  function tabKeydown(event: KeyboardEvent) {
    // Arrows move from the focused tab and select it (automatic activation).
    const from = (event.currentTarget as HTMLElement).dataset.connectionsTab as ConnectionTab;
    const next = nextConnectionTab(from || tab, event.key);
    if (!next) return;
    event.preventDefault();
    void select(next, true);
  }

  function typedManualToken(): boolean {
    // Legacy manual administration cards own their token fields. Keep their
    // existing close guard; guided setup also reports hidden drafts above.
    return Array.from(publicPanel?.querySelectorAll<HTMLInputElement>('input[type="password"]') || []).some((input) => input.value);
  }

  function requestClose() {
    if (closing) return;
    if (secretPending) {
      closing = { title: 'Close without saving the webhook URL?', message: 'The new webhook URL is shown only once. Copy it first, or rotate the webhook later to get a new one.', confirm: 'Close anyway', cancel: 'Keep open' };
    } else if (emailDirty || webhookDirty || publicDirty || typedManualToken()) {
      const what = [emailDirty && 'inbox name', webhookDirty && 'webhook name', (publicDirty || typedManualToken()) && 'public access setup'].filter(Boolean).join(' and ');
      closing = { title: 'Discard unsaved changes?', message: `Your unsaved ${what} will be lost.`, confirm: 'Discard', cancel: 'Keep editing' };
    } else {
      onClose();
      return;
    }
    closeReturn = document.activeElement instanceof HTMLElement && dialog?.contains(document.activeElement) ? document.activeElement : closeButton;
  }

  async function keepOpen() {
    closing = null;
    await tick();
    (closeReturn?.isConnected ? closeReturn : closeButton)?.focus();
  }

  function keydown(event: KeyboardEvent) {
    if (closing) return;
    if (event.key === 'Escape') {
      // Help sits over the manager and closes itself (window listener).
      if (helpOpen) return;
      event.preventDefault();
      event.stopPropagation();
      const menu = document.activeElement instanceof HTMLElement ? document.activeElement.closest<HTMLDetailsElement>('details.row-menu[open]') : null;
      if (menu && dialog.contains(menu)) {
        menu.open = false;
        menu.querySelector('summary')?.focus();
        return;
      }
      requestClose();
      return;
    }
    if (event.key === 'Tab') containTab(event, (helpOpen && dialog.querySelector<HTMLElement>('[data-help-drawer]')) || dialog);
    // Page shortcuts (/ search, [ sidebar…) must not act behind the modal.
    if (!event.ctrlKey && !event.metaKey) event.stopPropagation();
  }

  function publicChanged() {
    inboxesKey += 1;
    webhooks?.refresh();
    onPublicAccessChanged();
  }

  onMount(() => {
    void tick().then(() => {
      const target = user.admin ? dialog?.querySelector<HTMLElement>(`#connections-tab-${tab}`) : closeButton;
      (target || dialog)?.focus();
    });
  });

  onDestroy(() => {
    unsubscribeHelp();
    // The help drawer lives inside the manager; do not leave it requested.
    if (helpOpen) helpRequest.set(null);
  });
</script>

<div class="connections-backdrop" role="presentation" on:click|self={requestClose}></div>
<div bind:this={dialog} class="ticket-integrations connections-manager" role="dialog" aria-modal="true" aria-labelledby="ticket-integrations-heading" aria-describedby="ticket-integrations-summary" tabindex="-1" on:keydown={keydown} data-connections-manager>
  <header class="connections-head">
    <div>
      <h2 id="ticket-integrations-heading">Connect apps</h2>
      <p id="ticket-integrations-summary">Monitoring, CI, forms and scripts can open tickets. New ones land in <strong>Needs triage</strong>.</p>
    </div>
    <button bind:this={closeButton} class="icon-button connections-close" type="button" aria-label="Close" on:click={requestClose}>✕</button>
  </header>

  {#if user.admin}
    <div class="connections-tabs" role="tablist" aria-label="Connection types">
      {#each connectionTabs as item (item.id)}
        <button
          id={`connections-tab-${item.id}`}
          class="connections-tab"
          type="button"
          role="tab"
          aria-selected={tab === item.id}
          aria-controls={`connections-panel-${item.id}`}
          tabindex={tab === item.id ? 0 : -1}
          data-connections-tab={item.id}
          on:click={() => select(item.id)}
          on:keydown={tabKeydown}
        ><span>{item.label}</span>{#if tabMeta[item.id].label}<span class={`connections-tab-meta tone-${tabMeta[item.id].tone}`}>{tabMeta[item.id].label}</span>{/if}</button>
      {/each}
    </div>
  {/if}

  <div class="connections-body">
    {#if user.admin}
      <div id="connections-panel-email" class="connections-panel" role="tabpanel" aria-labelledby="connections-tab-email" tabindex="-1" hidden={tab !== 'email'}>
        <EmailInboxes refreshKey={inboxesKey} bind:loaded={emailLoaded} bind:emailOn bind:activeCount={inboxCount} bind:dirty={emailDirty} onSetUpEmail={() => select('public', true)} />
      </div>
      <div id="connections-panel-webhooks" class="connections-panel" role="tabpanel" aria-labelledby="connections-tab-webhooks" tabindex="-1" hidden={tab !== 'webhooks'}>
        <TicketWebhooks bind:this={webhooks} bind:activeCount={webhookCount} bind:secretPending bind:dirty={webhookDirty} />
      </div>
      <div bind:this={publicPanel} id="connections-panel-public" class="connections-panel" role="tabpanel" aria-labelledby="connections-tab-public" tabindex="-1" hidden={tab !== 'public'}>
        <PublicAccessPanel bind:dirty={publicDirty} onChanged={publicChanged} />
      </div>
    {:else}
      <section class="conn-section connections-readonly" aria-labelledby="connections-readonly-heading">
        <header class="conn-head">
          <div>
            <h3 id="connections-readonly-heading">Webhooks and email</h3>
            <p>Ask a workspace administrator to create a webhook URL or email inbox for your app. You can read how apps send tickets here.</p>
          </div>
          <div class="conn-actions"><button class="text-button" type="button" aria-expanded={guideOpen} on:click={() => { guideOpen = !guideOpen; }}>How to send</button></div>
        </header>
        {#if guideOpen}<TicketWebhookGuide admin={false} />{/if}
      </section>
    {/if}
  </div>
  <HelpDrawer />
</div>

{#if closing}
  <ConfirmDialog title={closing.title} message={closing.message} confirmLabel={closing.confirm} cancelLabel={closing.cancel} onConfirm={() => { closing = null; onClose(); }} onCancel={keepOpen} />
{/if}
