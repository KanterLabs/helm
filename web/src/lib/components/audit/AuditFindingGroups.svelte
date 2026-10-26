<script lang="ts">
  import type { AuditFinding, AuditVerdict, AuditReviewState, Column, SemanticState } from '../../types';
  import { stateLabels, verdictLabels, reviewLabels, semanticStates } from './helpers';
  import type { AuditHelpers } from './helpers';
  export let helpers: AuditHelpers;
  export let groupedFindings: { verdict: AuditVerdict; label: string; items: AuditFinding[] }[];
  export let columns: Column[];
  export let editingFindingId: string;
  export let destinationDraft: SemanticState | '';
  export let activeMutation: string;
  export let saveDestination: (finding: AuditFinding) => Promise<void>;
  export let cancelDestination: () => void;
  export let editDestination: (finding: AuditFinding) => void;
  export let approveFinding: (finding: AuditFinding) => Promise<void>;
  export let dismissFinding: (finding: AuditFinding) => Promise<void>;
  export let openPreview: (finding: AuditFinding) => void;
</script>

      {#each groupedFindings as group}
        <section class={`audit-group audit-group-${group.verdict}`} aria-labelledby={`audit-group-${group.verdict}`}>
          <div class="audit-group-heading"><div><h2 id={`audit-group-${group.verdict}`}>{group.label}</h2><p>{group.verdict === 'correct' ? 'No board action was suggested.' : group.verdict === 'needs_attention' ? 'Review the evidence and decide whether this finding is useful.' : 'Approve a destination, then preview and explicitly apply the move.'}</p></div><span>{group.items.length}</span></div>
          {#if group.items.length}
            <div class="audit-finding-list">
              {#each group.items as finding (finding.id)}
                <article class="audit-finding" class:changed={helpers.changedSinceAudit(finding)}>
                  <div class="audit-finding-main">
                    <div class="audit-finding-top"><span class="task-key">{helpers.taskLabel(finding)}</span><span class={`audit-verdict ${helpers.verdictClass(finding.verdict)}`}>{verdictLabels[finding.verdict as AuditVerdict] || finding.verdict}</span><span class={`audit-review-state ${helpers.reviewClass(finding.review_state)}`}>{reviewLabels[finding.review_state as AuditReviewState] || finding.review_state}</span></div>
                    <h3>{helpers.findingTask(finding)?.title || finding.task_id}</h3>
                    <div class="audit-route"><span>{helpers.sourceLabel(finding)}</span><span aria-hidden="true">→</span><strong>{helpers.destinationLabel(finding)}</strong></div>
                    <p class="audit-reason">{finding.reason || 'No reason provided.'}</p>
                    {#if helpers.evidenceFor(finding).length}<div class="audit-evidence"><span class="optional">Evidence</span>{#each helpers.evidenceFor(finding) as evidence}<span>{evidence}</span>{/each}</div>{/if}
                    <div class="audit-finding-meta"><span>{helpers.confidenceLabel(finding.confidence)}</span><span>Captured <strong>{helpers.findingVersionLabel(finding)}</strong></span><span>Current <strong>{helpers.currentVersionLabel(finding)}</strong></span><span>{helpers.changedSinceAudit(finding) ? 'Changed since audit' : `Current: ${helpers.currentLabel(finding)}`}</span></div>
                    {#if helpers.changedSinceAudit(finding)}<div class="audit-changed-warning" role="alert"><span aria-hidden="true">!</span><span>This task changed after the audit. Approval and apply are locked until a fresh audit captures the current version.</span></div>{/if}
                  </div>
                  <div class="audit-finding-actions">
                    {#if editingFindingId === finding.id}
                      <label class="audit-destination-editor">Suggested column<select data-audit-destination={finding.id} aria-label={`Suggested column for ${helpers.taskLabel(finding)}`} bind:value={destinationDraft}>{#each semanticStates as state}<option value={state}>{columns.find((column) => column.semantic_state === state)?.name || stateLabels[state]}</option>{/each}<option value="">No move suggested</option></select></label>
                      <div class="audit-action-row"><button class="button primary compact-button" type="button" disabled={activeMutation === finding.id} on:click={() => void saveDestination(finding)}>Save destination</button><button class="button quiet-button compact-button" type="button" disabled={activeMutation === finding.id} on:click={cancelDestination}>Cancel</button></div>
                    {:else}
                      <button class="button quiet-button compact-button" type="button" disabled={!helpers.auditReadyForReview()} on:click={() => editDestination(finding)}>Edit destination</button>
                    {/if}
                    <div class="audit-action-row"><button class="button quiet-button compact-button" type="button" disabled={!helpers.auditReadyForReview() || activeMutation === finding.id || helpers.changedSinceAudit(finding)} on:click={() => void approveFinding(finding)}>{finding.review_state === 'approved' ? 'Approved' : 'Approve'}</button><button class="button quiet-button compact-button" type="button" disabled={!helpers.auditReadyForReview() || activeMutation === finding.id || finding.review_state === 'dismissed'} on:click={() => void dismissFinding(finding)}>{finding.review_state === 'dismissed' ? 'Dismissed' : 'Dismiss'}</button></div>
                    {#if finding.review_state === 'approved'}<button class="button primary compact-button" type="button" disabled={!helpers.auditReadyForReview() || helpers.changedSinceAudit(finding)} title={helpers.applyBlockReason(finding)} on:click={() => openPreview(finding)}>Preview approved finding</button>{/if}
                  </div>
                </article>
              {/each}
            </div>
          {:else}<div class="audit-group-empty">No findings in this group.</div>{/if}
        </section>
      {/each}
