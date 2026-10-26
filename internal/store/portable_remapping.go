package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

func portableStableID(entity, source string) string {
	sum := sha256.Sum256([]byte(PortableFormat + "\x00" + entity + "\x00" + source))
	return hex.EncodeToString(sum[:16])
}

func portableCandidateID(entity, source string, used map[string]struct{}) string {
	for attempt := 0; ; attempt++ {
		candidate := portableCandidateIDAt(entity, source, attempt)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func portableCandidateIDAt(entity, source string, attempt int) string {
	if attempt > 0 {
		source += ":" + strconv.Itoa(attempt+1)
	}
	return portableStableID(entity, source)
}

// portableFindMatchingCandidate searches the deterministic remap sequence for
// a previously imported copy. Looking beyond the first candidate matters when
// an unrelated destination row already occupied that candidate during the
// original import: retries must find the copy that was actually inserted,
// rather than creating a new suffix each time.
func portableFindMatchingCandidate(entity, source string, inspect func(string) (occupied, matching bool, err error)) (string, bool, error) {
	for attempt := 0; ; attempt++ {
		candidate := portableCandidateIDAt(entity, source, attempt)
		if candidate == source {
			continue
		}
		occupied, matched, err := inspect(candidate)
		if err != nil {
			return "", false, err
		}
		if !occupied {
			// The original import would have selected the first free
			// candidate, so no later suffix can be its remap.
			return "", false, nil
		}
		if matched {
			return candidate, true, nil
		}
	}
}

func portableSuffix(base, suffix string, max int) string {
	base = strings.TrimSpace(base)
	if len(base)+len(suffix) > max {
		base = base[:max-len(suffix)]
	}
	base = strings.TrimRight(base, "-_")
	if base == "" {
		base = "IMPORT"
	}
	return base + suffix
}

func portableUniqueKey(base, sourceID string, used map[string]struct{}) string {
	base = strings.ToUpper(strings.TrimSpace(base))
	for attempt := 0; attempt < 1000; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = portableSuffix(base, "-"+strconv.Itoa(attempt+1), 16)
		}
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
	hash := sha256.Sum256([]byte(sourceID))
	return portableSuffix("IMPORT", "-"+hex.EncodeToString(hash[:])[:8], 16)
}

func portableUniqueSlug(base, sourceID string, used map[string]struct{}) string {
	base = strings.ToLower(strings.TrimSpace(base))
	for attempt := 0; attempt < 1000; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = portableSuffix(base, "-"+strconv.Itoa(attempt+1), 64)
		}
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
	hash := sha256.Sum256([]byte(sourceID))
	return portableSuffix("import", "-"+hex.EncodeToString(hash[:])[:8], 64)
}

func portableStringMap(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
