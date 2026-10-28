package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

func (s *Server) githubAuditCorrections(w http.ResponseWriter, r *http.Request, identity auth.Identity, job store.GithubAuditJob) {
	var input struct {
		AssessmentVersion int                                `json:"assessment_version"`
		Preview           *bool                              `json:"preview"`
		Items             []store.GithubAuditCorrectionInput `json:"items"`
	}
	fields, err := decodeJSONObject(r, &input)
	if err != nil || len(fields) != 3 || input.AssessmentVersion != 2 || input.Preview == nil || len(input.Items) == 0 || len(input.Items) > 200 {
		s.writeError(w, 400, "invalid_request", "assessment_version=2, explicit preview and 1–200 unique items are required", nil)
		return
	}
	seen := map[string]bool{}
	// Validate the entire shape/scope before processing any item. Missing,
	// malformed or legacy assessments never silently route to legacy application.
	for _, item := range input.Items {
		if item.FindingID == "" || len(item.FindingID) > 200 || seen[item.FindingID] || len(item.ConfirmationToken) > 4096 || len(item.Reason) > 10000 || len(item.Resolution) > 30 {
			s.writeError(w, 400, "invalid_request", "correction items must be bounded and unique", nil)
			return
		}
		seen[item.FindingID] = true
		f, e := s.Store.GetAuditFinding(r.Context(), item.FindingID)
		if e != nil {
			s.writeStoreError(w, e)
			return
		}
		if f.AuditID != job.AuditID || f.Assessment == nil || f.Assessment.AssessmentVersion != 2 {
			s.writeError(w, 400, "invalid_request", "every item must be a v2 finding in this operational audit", nil)
			return
		}
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !*input.Preview && (key == "" || len(key) > 200) {
		s.writeError(w, 400, "idempotency_key_required", "Apply requires a bounded Idempotency-Key; reuse it after an unknown network outcome", nil)
		return
	}
	if !s.admitMutationRate(w, r, identity) {
		return
	}
	raw, _ := json.Marshal(input)
	sum := sha256.Sum256(raw)
	requestHash := hex.EncodeToString(sum[:])
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	results := make([]store.GithubAuditCorrectionResult, 0, len(input.Items))
	for _, item := range input.Items {
		fail := store.GithubAuditCorrectionResult{AuditID: job.AuditID, ActorID: identity.Actor.ID, FindingID: item.FindingID, Outcome: "failed", Message: "Outcome could not be confirmed; retry the identical request with the same Idempotency-Key"}
		if !*input.Preview {
			receipt, ok, e := s.Store.ReadGithubAuditCorrectionReceipt(ctx, job.AuditID, identity.Actor.ID, key, requestHash, item.FindingID)
			if e != nil {
				if errors.Is(e, store.ErrConflict) {
					s.writeStoreError(w, e)
					return
				}
				results = append(results, fail)
				continue
			}
			if ok {
				results = append(results, receipt)
				continue
			}
		}
		unavailable := ""
		// Remote checks run per item, then the store repeats local checks under its
		// writer lock. Snapshot generations are checked again inside that transaction.
		e := s.GithubAudits.authorizedJob(ctx, job)
		if e == nil && s.GithubAudits.guard != nil {
			e = s.GithubAudits.guard(ctx, identity.Actor.ID, job.ProjectID)
		}
		var evidence store.GithubAuditEvidence
		if e == nil {
			evidence, e = s.Store.GetGithubAuditEvidence(ctx, job.AuditID)
		}
		if e == nil {
			e = s.GithubAudits.evidenceAccess(ctx, job.ProjectID, evidence)
		}
		if e == nil {
			e = s.GithubAudits.fresh(ctx, evidence)
		}
		if e != nil {
			unavailable = "Source, PR/check, selection or authorization changed or is unavailable; reconnect and re-audit this card"
		}
		if *input.Preview {
			result, e := s.Store.PreviewGithubAuditCorrection(ctx, job.AuditID, identity.Actor.ID, item)
			if e != nil {
				results = append(results, fail)
				continue
			}
			if unavailable != "" {
				result.Outcome = "skipped"
				result.Message = unavailable
			}
			if result.Outcome == "eligible" {
				expires := time.Now().Add(5 * time.Minute)
				result.ExpiresAt = expires.UTC().Format(time.RFC3339)
				result.ConfirmationToken = signCorrectionProof(identity.Actor.ID, job.AuditID, item.FindingID, result.Fingerprint, expires.Unix())
			}
			results = append(results, result)
			continue
		}
		expected := verifyCorrectionProof(item.ConfirmationToken, identity.Actor.ID, job.AuditID, item.FindingID)
		result, e := s.Store.ApplyGithubAuditCorrection(ctx, job.AuditID, identity.Actor.ID, key, requestHash, item, expected, unavailable, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if expected != "" && verifyCorrectionProof(item.ConfirmationToken, identity.Actor.ID, job.AuditID, item.FindingID) != expected {
				return &store.Error{Kind: store.ErrConflict, Message: "Preview expired; refresh the preview"}
			}
			if job.ProviderGeneration != s.GithubAudits.providerGeneration() {
				return &store.Error{Kind: store.ErrConflict, Message: "GitHub connection changed; reconnect and re-audit this card"}
			}
			return nil
		})
		if e != nil {
			results = append(results, fail)
			continue
		}
		results = append(results, result)
	}
	s.writeJSON(w, 200, map[string]any{"data": results})
}
