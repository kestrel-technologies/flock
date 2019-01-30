package lifecycle

// planJoiner provisions an account in every enabled tenant the person's role is entitled to.
func planJoiner(ev Event, enabled map[string]bool) []Action {
	var out []Action
	for _, t := range sortedTenants(enabled) {
		if ev.Person.Entitled(t) {
			out = append(out, Action{Kind: Provision, Tenant: t, Person: ev.Person, Event: ev.ID})
		}
	}
	return out
}
