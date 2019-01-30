package lifecycle

import (
	"fmt"
	"sort"
)

// Plan returns the actions for one HR event against the enabled tenants (name -> true).
// Actions are ordered by tenant name so audit logs are stable.
func Plan(ev Event, enabled map[string]bool) ([]Action, error) {
	if ev.Person.Email == "" {
		return nil, fmt.Errorf("event %s: person has no email", ev.ID)
	}
	var out []Action
	switch ev.Type {
	case Joiner:
		out = planJoiner(ev, enabled)
	case Leaver:
		out = planLeaver(ev, enabled)
	default:
		return nil, fmt.Errorf("event %s: unknown event type %q", ev.ID, ev.Type)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tenant < out[j].Tenant })
	return out, nil
}

func sortedTenants(enabled map[string]bool) []string {
	names := make([]string, 0, len(enabled))
	for n, on := range enabled {
		if on {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}
