package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/codexruntime"
	"github.com/KanterLabs/helm/internal/store"
)

func (s *Server) lunaRuns(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	limit := 25
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer from 1 to 100", nil)
			return
		}
		limit = value
	}
	runs, err := s.Store.ListLunaRuns(r.Context(), identity.Actor.ID, limit)
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeCollection(w, runs, "")
}

func (s *Server) startLunaRun(ctx context.Context, input store.LunaRunStart) *store.LunaRun {
	run, err := s.Store.StartLunaRun(ctx, input)
	if err != nil {
		s.logJSON(map[string]any{"level": "error", "msg": "luna history start failed", "error_class": classifyError(err)})
		return nil
	}
	return &run
}

func (s *Server) lunaRunStepCallback(run *store.LunaRun) func(codexruntime.RunStep) {
	if run == nil {
		return nil
	}
	return func(step codexruntime.RunStep) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.Store.AppendLunaRunStep(ctx, run.ID, store.LunaRunStepInput{Kind: step.Kind}); err != nil {
			s.logJSON(map[string]any{"level": "error", "msg": "luna history step failed", "error_class": classifyError(err)})
		}
	}
}

func (s *Server) appendLunaRunStep(run *store.LunaRun, kind string) {
	if run == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Store.AppendLunaRunStep(ctx, run.ID, store.LunaRunStepInput{Kind: kind}); err != nil {
		s.logJSON(map[string]any{"level": "error", "msg": "luna history step failed", "error_class": classifyError(err)})
	}
}

func (s *Server) finishLunaRun(run *store.LunaRun, result codexruntime.RunResult, outcome, detail string, started time.Time) {
	if run == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Store.AppendLunaRunStep(ctx, run.ID, store.LunaRunStepInput{Kind: "outcome"}); err != nil {
		s.logJSON(map[string]any{"level": "error", "msg": "luna history outcome step failed", "error_class": classifyError(err)})
	}
	err := s.Store.FinishLunaRun(ctx, run.ID, store.LunaRunFinish{
		Outcome: outcome, ThreadID: result.ThreadID, TurnID: result.TurnID,
		DurationMS: store.LunaRunDuration(started), OutputBytes: int64(len(result.Output)),
		Detail: safeLunaRunDetail(detail),
	})
	if err != nil {
		s.logJSON(map[string]any{"level": "error", "msg": "luna history finish failed", "error_class": classifyError(err)})
	}
}

func safeLunaRunDetail(value string) string {
	value = strings.TrimSpace(strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return ' '
		}
		return char
	}, value))
	runes := []rune(value)
	if len(runes) > 500 {
		value = string(runes[:500])
	}
	return value
}
