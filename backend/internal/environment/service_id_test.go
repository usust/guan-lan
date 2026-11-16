package environment

import "testing"

func TestServiceIDsStableAndIndependentOfNames(t *testing.T) {
	m := Manifest{Services: []Service{{Key: "web", Name: "财务系统"}, {Key: "db", Name: "财务系统"}}}
	m.AssignServiceIDs("package-a")
	first := m.Services[0].ID
	if len(first) != 36 || first == m.Services[1].ID {
		t.Fatal("invalid or duplicate service IDs")
	}
	m.Services[0].Name = "新名称"
	m.AssignServiceIDs("package-a")
	if m.Services[0].ID != first {
		t.Fatal("service ID changed on rename")
	}
	if m.Services[0].Key != "web" {
		t.Fatal("network alias changed")
	}
	m.Services[0].ID = ""
	m.AssignServiceIDs("package-b")
	if m.Services[0].ID == first {
		t.Fatal("different packages share service ID")
	}
}
