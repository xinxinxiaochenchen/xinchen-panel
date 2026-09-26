package routing

import (
	"strconv"
	"strings"

	"controlplane/internal/catalog"
)

func RuleCursor(rule Rule) string { return strconv.Itoa(rule.Priority) + ":" + rule.ID }

func ParseRuleCursor(cursor string) (int, string, error) {
	if cursor == "" {
		return 0, "", nil
	}
	priorityText, id, ok := strings.Cut(cursor, ":")
	priority, err := strconv.Atoi(priorityText)
	if !ok || err != nil || priority < 1 || priority > 1000000 || !catalog.ValidID(id) {
		return 0, "", ValidationError{"cursor", "invalid routing rule cursor"}
	}
	return priority, strings.ToLower(id), nil
}
