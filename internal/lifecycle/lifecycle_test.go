package lifecycle

import (
	"reflect"
	"testing"
)

var enabled = map[string]bool{"corp-prod": true, "directory": true, "paused-prod": false}

func kinds(actions []Action) []string {
	var out []string
	for _, a := range actions {
		out = append(out, string(a.Kind)+":"+a.Tenant)
	}
	return out
}

func TestJoinerProvisionsEntitledEnabledTenants(t *testing.T) {
	ev := Event{ID: "hr-1", Type: Joiner, Person: Person{
		Email: "test.user1@kestrel.example", Entitlements: []string{"directory", "corp-prod", "paused-prod"},
	}}
	got, err := Plan(ev, enabled)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"provision:corp-prod", "provision:directory"}
	if !reflect.DeepEqual(kinds(got), want) {
		t.Errorf("joiner actions = %v, want %v", kinds(got), want)
	}
}

func TestLeaverDeprovisionsEveryEnabledTenant(t *testing.T) {
	ev := Event{ID: "hr-2", Type: Leaver, Person: Person{Email: "test.user1@kestrel.example"}}
	got, err := Plan(ev, enabled)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"deprovision:corp-prod", "deprovision:directory"}
	if !reflect.DeepEqual(kinds(got), want) {
		t.Errorf("leaver actions = %v, want %v", kinds(got), want)
	}
}

func TestMoverDiffsEntitlements(t *testing.T) {
	ev := Event{ID: "hr-3", Type: Mover,
		Previous: &Person{Email: "test.user1@kestrel.example", Entitlements: []string{"corp-prod", "directory"}},
		Person:   Person{Email: "test.user1@kestrel.example", Entitlements: []string{"directory"}},
	}
	got, err := Plan(ev, enabled)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"deprovision:corp-prod", "update:directory"}
	if !reflect.DeepEqual(kinds(got), want) {
		t.Errorf("mover actions = %v, want %v", kinds(got), want)
	}
}

func TestPlanRejectsBadEvents(t *testing.T) {
	for _, ev := range []Event{
		{ID: "x1", Type: Joiner},
		{ID: "x2", Type: Mover, Person: Person{Email: "a@kestrel.example"}},
		{ID: "x3", Type: "rehire", Person: Person{Email: "a@kestrel.example"}},
	} {
		if _, err := Plan(ev, enabled); err == nil {
			t.Errorf("event %s: expected an error", ev.ID)
		}
	}
}
