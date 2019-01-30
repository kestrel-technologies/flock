package lifecycle

// planLeaver deprovisions the person in every enabled tenant, entitled or not: an account may have
// been created by hand, and KSS requires leavers to be deactivated everywhere within 24 hours.
// Connectors treat a missing account as success.
func planLeaver(ev Event, enabled map[string]bool) []Action {
	var out []Action
	for _, t := range sortedTenants(enabled) {
		out = append(out, Action{Kind: Deprovision, Tenant: t, Person: ev.Person, Event: ev.ID})
	}
	return out
}
