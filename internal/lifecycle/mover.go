package lifecycle

// planMover compares the old and new entitlements: tenants gained are provisioned, tenants lost are
// deprovisioned, and tenants kept get an attribute update (title, department, manager).
func planMover(ev Event, enabled map[string]bool) []Action {
	var out []Action
	for _, t := range sortedTenants(enabled) {
		before, after := ev.Previous.Entitled(t), ev.Person.Entitled(t)
		switch {
		case after && !before:
			out = append(out, Action{Kind: Provision, Tenant: t, Person: ev.Person, Event: ev.ID})
		case before && !after:
			out = append(out, Action{Kind: Deprovision, Tenant: t, Person: ev.Person, Event: ev.ID})
		case before && after:
			out = append(out, Action{Kind: Update, Tenant: t, Person: ev.Person, Event: ev.ID})
		}
	}
	return out
}
