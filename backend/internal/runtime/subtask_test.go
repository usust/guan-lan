package runtime

import (
	"huashan/internal/environment"
	"testing"
)

func TestScopedManifestIncludesOnlyBindingsAndDependencies(t *testing.T) {
	manifest := environment.Manifest{Services: []environment.Service{
		{Key: "db"}, {Key: "web", DependsOn: []string{"db"}}, {Key: "other"},
	}}
	got, err := scopedManifest(manifest, []string{"web"})
	if err != nil || len(got.Services) != 2 || got.Services[0].Key != "db" || got.Services[1].Key != "web" {
		t.Fatal("incorrect service scope", got, err)
	}
	other, err := scopedManifest(manifest, []string{"other"})
	if err != nil || len(other.Services) != 1 || other.Services[0].Key != "other" {
		t.Fatal("subtasks share services", other, err)
	}
	if len(manifest.Services) != 3 {
		t.Fatal("source manifest changed")
	}
	if _, err := scopedManifest(manifest, []string{"missing"}); err == nil {
		t.Fatal("missing binding accepted")
	}
}
