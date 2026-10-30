package httpapi

import (
	"errors"
	"fmt"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Server) searchFilterFromView(r *http.Request, identity auth.Identity, view store.SavedView) (store.SearchFilter, error) {
	filter := store.SearchFilter{ProjectIDs: scopedProjectIDs(identity), Sort: append([]store.SearchSort(nil), view.Sort...)}
	var explicitProjects []string
	explicitProjectsSet := false
	var dueFromKey, dueToKey string
	for key, raw := range view.Filters {
		canonicalKey := strings.ToLower(strings.TrimSpace(key))
		if !savedViewFilterKeys[canonicalKey] {
			return store.SearchFilter{}, fmt.Errorf("view filter %s is not supported", key)
		}
		if canonicalKey == "project_ids" || canonicalKey == "projects" {
			projects, err := s.resolveSavedViewProjects(r, identity, raw)
			if err != nil {
				return store.SearchFilter{}, err
			}
			if explicitProjectsSet {
				explicitProjects = intersectProjectIDs(explicitProjects, projects)
			} else {
				explicitProjects = projects
				explicitProjectsSet = true
			}
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return store.SearchFilter{}, fmt.Errorf("view filter %s must be a string", key)
		}
		value, err := savedViewFilterValue(canonicalKey, value)
		if err != nil {
			return store.SearchFilter{}, err
		}
		switch canonicalKey {
		case "q", "query":
			filter.Query = value
		case "key":
			filter.Key = value
		case "title":
			filter.Title = value
		case "description":
			filter.Description = value
		case "label":
			filter.Label = value
		case "state":
			if _, ok := semanticStates[value]; !ok {
				return store.SearchFilter{}, fmt.Errorf("state is invalid")
			}
			filter.State = value
		case "priority":
			if _, ok := taskPriorities[value]; !ok {
				return store.SearchFilter{}, fmt.Errorf("priority is invalid")
			}
			filter.Priority = value
		case "assignee":
			filter.Assignee = value
		case "claim_owner", "claimed_by":
			filter.ClaimOwner = value
		case "release_id":
			resolved, err := s.resolveGlobalReleaseFilter(r, identity, value)
			if err != nil {
				return store.SearchFilter{}, err
			}
			filter.ReleaseID = resolved
		case "project", "project_id":
			project, err := s.Store.GetProject(r.Context(), value)
			if err != nil {
				return store.SearchFilter{}, err
			}
			if !identity.CanProject(project.ID) {
				return store.SearchFilter{}, &store.Error{Kind: store.ErrForbidden, Message: "view is not available to this actor"}
			}
			filter.Project = project.ID
			if explicitProjectsSet {
				explicitProjects = intersectProjectIDs(explicitProjects, []string{project.ID})
			} else {
				explicitProjects = []string{project.ID}
				explicitProjectsSet = true
			}
		case "due_from", "due_after":
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return store.SearchFilter{}, fmt.Errorf("%s must be RFC3339", key)
			}
			if dueFromKey != "" {
				return store.SearchFilter{}, fmt.Errorf("%s and %s cannot both be supplied", dueFromKey, key)
			}
			dueFromKey = key
			filter.DueFrom = &parsed
		case "due_to", "due_before":
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return store.SearchFilter{}, fmt.Errorf("%s must be RFC3339", key)
			}
			if dueToKey != "" {
				return store.SearchFilter{}, fmt.Errorf("%s and %s cannot both be supplied", dueToKey, key)
			}
			dueToKey = key
			filter.DueTo = &parsed
		}
	}
	if explicitProjectsSet {
		if filter.ProjectIDs == nil {
			filter.ProjectIDs = explicitProjects
		} else {
			filter.ProjectIDs = intersectProjectIDs(filter.ProjectIDs, explicitProjects)
		}
	}
	if filter.DueFrom != nil && filter.DueTo != nil && filter.DueFrom.After(*filter.DueTo) {
		return store.SearchFilter{}, errors.New("due_from must be before due_to")
	}
	return filter, nil
}

var savedViewFilterKeys = map[string]bool{
	"q": true, "query": true, "key": true, "title": true, "description": true,
	"label": true, "state": true, "priority": true, "assignee": true,
	"claim_owner": true, "claimed_by": true, "project": true, "project_id": true,
	"project_ids": true, "projects": true, "due_from": true, "due_after": true,
	"due_to": true, "due_before": true, "release_id": true,
}

func savedViewFilterValue(key, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("view filter %s must not be empty", key)
	}
	if utf8.RuneCountInString(value) > 200 {
		return "", fmt.Errorf("view filter %s is too long", key)
	}
	return value, nil
}

func (s *Server) resolveSavedViewProjects(r *http.Request, identity auth.Identity, raw any) ([]string, error) {
	values, ok := raw.([]any)
	if !ok {
		if strings, stringOK := raw.([]string); stringOK {
			values = make([]any, len(strings))
			for i := range strings {
				values[i] = strings[i]
			}
		} else {
			return nil, errors.New("view project_ids must be an array")
		}
	}
	if len(values) > 200 {
		return nil, errors.New("view project_ids has too many projects")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, rawValue := range values {
		value, ok := rawValue.(string)
		if !ok {
			return nil, errors.New("view project_ids must contain identifiers")
		}
		value, err := savedViewFilterValue("project_id", value)
		if err != nil {
			return nil, err
		}
		project, err := s.Store.GetProject(r.Context(), value)
		if err != nil {
			return nil, err
		}
		if !identity.CanProject(project.ID) {
			return nil, &store.Error{Kind: store.ErrForbidden, Message: "view is not available to this actor"}
		}
		if _, ok := seen[project.ID]; ok {
			continue
		}
		seen[project.ID] = struct{}{}
		result = append(result, project.ID)
	}
	return result, nil
}

func intersectProjectIDs(left, right []string) []string {
	if left == nil {
		return append([]string(nil), right...)
	}
	if right == nil {
		return append([]string(nil), left...)
	}
	allowed := make(map[string]struct{}, len(right))
	for _, id := range right {
		allowed[id] = struct{}{}
	}
	result := make([]string, 0, len(left))
	for _, id := range left {
		if _, ok := allowed[id]; ok {
			result = append(result, id)
		}
	}
	return result
}
