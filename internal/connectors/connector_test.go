package connectors

import (
	"errors"
	"testing"

	"kestrel.example/flock/internal/lifecycle"
)

func TestAccountFor(t *testing.T) {
	p := lifecycle.Person{EmployeeID: "E100", Email: "Test.User1@kestrel.example", GivenName: "Test", FamilyName: "User1"}
	a := AccountFor(p, "corp.kestrel.example", "Standard User")
	if a.UserName != "test.user1@corp.kestrel.example" {
		t.Errorf("UserName = %s", a.UserName)
	}
	if a.FederationID != "test.user1@kestrel.example" || a.Email != a.FederationID {
		t.Errorf("FederationID = %s, Email = %s", a.FederationID, a.Email)
	}
	if !a.Active || a.Profile != "Standard User" || a.EmployeeID != "E100" {
		t.Errorf("account = %+v", a)
	}
}

func TestReportErr(t *testing.T) {
	if (Report{}).Err() != nil {
		t.Error("empty report should have no error")
	}
	r := Report{Errors: []error{errors.New("a"), errors.New("b")}}
	if r.Err() == nil {
		t.Error("expected joined error")
	}
}
