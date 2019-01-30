// Package lifecycle turns HR events (joiner, mover, leaver) into per-tenant connector actions.
//
// The HR feed publishes one event per change to a worker record. Plan maps an event to actions for
// the tenants that are enabled in config/tenants.yaml. Disabled tenants get no actions: their
// accounts are not touched until the tenant is enabled again and the reconciler catches up.
package lifecycle

import (
	"fmt"
	"time"
)

// EventType is the kind of HR change.
type EventType string

const (
	Joiner EventType = "joiner"
	Mover  EventType = "mover"
	Leaver EventType = "leaver"
)

// Person is a worker record as published by the HR feed.
type Person struct {
	EmployeeID   string   `json:"employee_id"`
	Email        string   `json:"email"`
	GivenName    string   `json:"given_name"`
	FamilyName   string   `json:"family_name"`
	Title        string   `json:"title"`
	Department   string   `json:"department"`
	CostCenter   string   `json:"cost_center"`
	ManagerEmail string   `json:"manager_email"`
	WorkerType   string   `json:"worker_type"`  // employee | contractor
	Entitlements []string `json:"entitlements"` // tenant names granted by role
}

// Entitled reports whether the person's role grants an account in tenant.
func (p Person) Entitled(tenant string) bool {
	for _, e := range p.Entitlements {
		if e == tenant {
			return true
		}
	}
	return false
}

// Event is one HR change. Previous is set for movers only.
type Event struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	Effective time.Time `json:"effective"`
	Person    Person    `json:"person"`
	Previous  *Person   `json:"previous,omitempty"`
}

// ActionKind is what a connector is asked to do.
type ActionKind string

const (
	Provision   ActionKind = "provision"
	Update      ActionKind = "update"
	Deprovision ActionKind = "deprovision"
)

// Action is one connector call for one tenant.
type Action struct {
	Kind   ActionKind
	Tenant string
	Person Person
	Event  string // HR event id, for the audit log
}

func (a Action) String() string {
	return fmt.Sprintf("%s %s in %s (event %s)", a.Kind, a.Person.Email, a.Tenant, a.Event)
}
