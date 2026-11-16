package environment

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimeOverridesPreserveImageIdentity(t *testing.T) {
	original := Manifest{FormatVersion: 1, Version: "1", Architecture: "amd64", Images: []Image{{Key: "web", File: "images/web.tar", SHA256: strings.Repeat("0", 64)}}, Services: []Service{{Key: "web", Image: "web", CPU: 1, MemoryMB: 128}}}
	clone := func() Manifest { b, _ := json.Marshal(original); var m Manifest; _ = json.Unmarshal(b, &m); return m }
	edited := clone()
	edited.Version = "2"
	edited.Images[0].Name = "财务系统镜像"
	edited.Services[0] = Service{Name: "财务系统", Key: "app", Image: "web", CPU: 2, MemoryMB: 256, Ports: []Port{{Port: 8080, Protocol: "tcp", Public: true, Label: "入口"}}}
	if err := edited.Validate(); err != nil {
		t.Fatal(err)
	}
	if !SamePackageImages(original, edited) {
		t.Fatal("runtime edits invalidated immutable package")
	}
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.Architecture = "arm64" },
		func(m *Manifest) { m.Images[0].SHA256 = strings.Repeat("1", 64) },
		func(m *Manifest) { m.Images[0].File = "images/replaced.tar" },
		func(m *Manifest) { m.Images[0].Key = "other" },
		func(m *Manifest) { m.Images = nil },
	} {
		changed := clone()
		mutate(&changed)
		if SamePackageImages(original, changed) {
			t.Fatal("changed immutable identity accepted")
		}
	}
}
