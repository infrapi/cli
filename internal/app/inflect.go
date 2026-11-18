package app

import "strings"

// Names is a resource's singular and plural from name, either one:
// singular or plural, as set, the other inferred. environments gives
// environment and environments, policy gives policy and policies. An
// English rule of thumb: when it gets one wrong (caches gives cach), set
// it.
func Names(name, singular, plural string) (string, string) {
	if singular == "" {
		switch {
		case plural != "" && name != "" && name != plural:
			singular = name
		case plural != "":
			singular = singularize(plural)
		case isPlural(name):
			singular = singularize(name)
		default:
			singular = name
		}
	}
	if plural == "" {
		if name != "" && name != singular {
			plural = name
		} else {
			plural = pluralize(singular)
		}
	}
	return singular, plural
}

// isPlural tells whether w reads as the plural of another word.
func isPlural(w string) bool {
	s := singularize(w)
	return s != w && pluralize(s) == w
}

func pluralize(w string) string {
	switch {
	case len(w) > 1 && strings.HasSuffix(w, "y") && !strings.ContainsRune("aeiou", rune(w[len(w)-2])):
		return w[:len(w)-1] + "ies"
	case hasSuffix(w, "s", "x", "z", "ch", "sh"):
		return w + "es"
	}
	return w + "s"
}

func singularize(w string) string {
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 3:
		return w[:len(w)-3] + "y"
	case hasSuffix(w, "sses", "xes", "zes", "ches", "shes"):
		return w[:len(w)-2]
	case strings.HasSuffix(w, "s") && !hasSuffix(w, "ss", "us", "is"):
		return w[:len(w)-1]
	}
	return w
}

func hasSuffix(w string, suffixes ...string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(w, s) {
			return true
		}
	}
	return false
}
