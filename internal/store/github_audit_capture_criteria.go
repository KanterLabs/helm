package store

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
)

var githubAuditBullet = regexp.MustCompile(`^(?:[-*+] |[0-9]+[.)] )(?:\[[ xX]\] )?`)

func githubAuditCondition(task GithubAuditTask, origin, sourceID, text string, line, end int) GithubAuditCondition {
	identity := fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%d\x00%s", task.ID, task.Version, origin, sourceID, line, text)
	sum := sha256.Sum256([]byte(identity))
	return GithubAuditCondition{ID: fmt.Sprintf("criterion:%x", sum[:16]), Text: text, Origin: origin, SourceID: sourceID, TaskVersion: task.Version, Line: line, EndLine: end}
}

// Only labeled goals and acceptance conditions are extracted. Unlabeled prose
// and titles remain task context; the evaluator must clarify ambiguous meaning.
// Quotes retain original spelling and punctuation, with structural list markers
// excluded and exact source line ranges retained.
func githubAuditDescriptionConditions(task GithubAuditTask) []GithubAuditCondition {
	conditions := []GithubAuditCondition{}
	section := ""
	lines := strings.Split(task.Description, "\n")
	continuationEnd := -1
	for i, line := range lines {
		if i <= continuationEnd {
			continue
		}
		trim := strings.TrimSpace(line)
		heading := strings.TrimSpace(strings.TrimLeft(trim, "#"))
		label := strings.ToLower(strings.Trim(heading, "*: "))
		if strings.HasPrefix(label, "goal:") {
			colon := strings.Index(heading, ":")
			trim = strings.TrimSpace(heading[colon+1:])
			if strings.HasPrefix(heading, "**") {
				trim = strings.TrimSpace(strings.TrimPrefix(trim, "**"))
			}
			section = "goal"
		} else {
			switch label {
			case "goal", "acceptance criteria", "acceptance conditions":
				section = "goal"
				if label != "goal" {
					section = "acceptance_criterion"
				}
				continue
			}
		}
		if trim == "" {
			continue
		}
		bullet := githubAuditBullet.FindString(trim)
		if section == "" {
			continue
		}
		if section == "acceptance_criterion" && bullet == "" {
			section = ""
			continue
		}
		if strings.HasPrefix(trim, "#") {
			section = ""
			continue
		}
		text := strings.TrimSpace(strings.TrimPrefix(trim, bullet))
		if text != "" {
			end := i
			for next := i + 1; next < len(lines); next++ {
				following := lines[next]
				trimmed := strings.TrimSpace(following)
				nextLabel := strings.ToLower(strings.Trim(strings.TrimSpace(strings.TrimLeft(trimmed, "#")), "*: "))
				if nextLabel == "goal" || strings.HasPrefix(nextLabel, "goal:") || nextLabel == "acceptance criteria" || nextLabel == "acceptance conditions" {
					break
				}
				if trimmed == "" || strings.HasPrefix(trimmed, "#") || githubAuditBullet.MatchString(trimmed) {
					break
				}
				if section == "acceptance_criterion" && len(following) == len(strings.TrimLeft(following, " \t")) {
					break
				}
				text += "\n" + following
				end = next
			}
			conditions = append(conditions, githubAuditCondition(task, section, "description", text, i+1, end+1))
			continuationEnd = end
		}
		if section == "goal" {
			section = ""
		}
	}
	return conditions
}
