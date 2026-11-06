package learn

import "fmt"

// todoTexts are the portal to-do templates per UI language. The names
// spliced in (courses, sessions, qualifications) come from translations.
var todoTexts = map[string]map[string]string{
	"en": {
		"finish":         "Finish %s",
		"progress":       "%d of %d activities done",
		"overdue":        "Overdue · %s",
		"renew":          "Renew %s",
		"expired_renew":  "Expired · renew by taking %s again",
		"expires_soon":   "%s expires soon",
		"renew_by":       "Renew by taking %s again",
		"retry":          "Retry %s",
		"not_passed":     "%s · not passed yet",
		"attempts_left":  "%s · %d attempts left",
		"attempt_left_1": "%s · 1 attempt left",
	},
	"nl": {
		"finish":         "Rond %s af",
		"progress":       "%d van %d activiteiten klaar",
		"overdue":        "Te laat · %s",
		"renew":          "Vernieuw %s",
		"expired_renew":  "Verlopen · vernieuw door %s opnieuw te volgen",
		"expires_soon":   "%s verloopt binnenkort",
		"renew_by":       "Vernieuw door %s opnieuw te volgen",
		"retry":          "Probeer %s opnieuw",
		"not_passed":     "%s · nog niet gehaald",
		"attempts_left":  "%s · nog %d pogingen",
		"attempt_left_1": "%s · nog 1 poging",
	},
}

// todoText formats a to-do template in the caller's first language that
// has templates, else English.
func todoText(langs Langs, key string, args ...any) string {
	texts := todoTexts["en"]
	for _, l := range langs {
		if t, ok := todoTexts[l]; ok {
			texts = t
			break
		}
	}
	return fmt.Sprintf(texts[key], args...)
}
