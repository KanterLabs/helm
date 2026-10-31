<svelte:options runes={true} />

<script lang="ts">
  // Local password change. Values live only in this component's memory and
  // are cleared after every outcome; they never reach storage or URLs.
  import { onDestroy } from 'svelte';
  import { api } from '../../api';
  import { ApiError } from '../../types';
  import { minimumPasswordError } from './access';
  import { accessGeneration, describeError, isUncertain } from './membersState.svelte';

  let { onChanged }: { onChanged?: () => void } = $props();

  let current = $state('');
  let next = $state('');
  let confirmation = $state('');
  let saving = $state(false);
  let error = $state('');
  let notice = $state('');
  let alive = true;
  onDestroy(() => { alive = false; current = ''; next = ''; confirmation = ''; });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    notice = '';
    error = !current ? 'Enter your current password.' : minimumPasswordError(next, confirmation);
    if (error) return;
    const generation = accessGeneration();
    saving = true;
    try {
      await api.changeOwnPassword(current, next);
      if (!alive || generation !== accessGeneration()) return;
      current = ''; next = ''; confirmation = '';
      notice = 'Password changed. Your other sessions were signed out; this browser stays signed in.';
      onChanged?.();
    } catch (failure) {
      if (!alive || generation !== accessGeneration()) return;
      if (failure instanceof ApiError && failure.code === 'invalid_credentials') {
        error = 'Your current password was not accepted.';
        current = '';
      } else if (isUncertain(failure)) {
        error = "We couldn't confirm whether your password changed. If your current password stops working, sign in with the new one.";
      } else {
        error = describeError(failure, 'Your password could not be changed.');
      }
    } finally {
      if (alive) saving = false;
    }
  }
</script>

<form class="mship-card mship-form" aria-label="Change password" onsubmit={submit}>
  <h3>Change password</h3>
  <label>Current password<input type="password" bind:value={current} autocomplete="current-password" required /></label>
  <div class="mship-row-fields">
    <label>New password<input type="password" bind:value={next} autocomplete="new-password" minlength="12" required aria-describedby="mship-password-hint" /></label>
    <label>Confirm new password<input type="password" bind:value={confirmation} autocomplete="new-password" minlength="12" required /></label>
  </div>
  <p class="mship-hint" id="mship-password-hint">At least 12 characters. Changing it signs out every other session.</p>
  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}
  {#if notice}<p class="mship-status" role="status">{notice}</p>{/if}
  <div class="mship-actions">
    <button class="button primary" type="submit" disabled={saving}>{#if saving}<span class="button-spinner"></span>{/if}Change password</button>
  </div>
</form>
