package environment

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func mergeFixture(t *testing.T, content string) (Manifest, []byte) {
	t.Helper()
	m := Manifest{FormatVersion: 1, Version: "1.0", Architecture: "amd64", Images: []Image{{Key: "image", File: "images/image.tar", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(content)))}}, Services: []Service{{Key: "container", Image: "image", CPU: 1, MemoryMB: 512}}}
	m.AssignServiceIDs(content)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	data, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		name string
		data []byte
	}{{"manifest.yaml", data}, {"images/image.tar", []byte(content)}} {
		if err = tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0600, Size: int64(len(entry.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err = tw.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err = tw.Close(); err != nil {
		t.Fatal(err)
	}
	return m, buf.Bytes()
}

func TestMergePackagesPreservesConfigurationAndArchive(t *testing.T) {
	base, raw := mergeFixture(t, "first")
	base.Services[0].Name = "财务系统"
	base.Services[0].CPU = 2
	originalID := base.Services[0].ID
	for _, content := range []string{"second", "third"} {
		added, source := mergeFixture(t, content)
		// Dependencies inside the incoming package must follow remapped keys.
		added.Services = append(added.Services, Service{Key: "worker", Image: "image", DependsOn: []string{"container"}, CPU: 1, MemoryMB: 128})
		merged, paths, err := Merge(base, added)
		if err != nil {
			t.Fatal(err)
		}
		if merged.Services[0].Name != "财务系统" || merged.Services[0].CPU != 2 || merged.Services[0].ID != originalID {
			t.Fatal("original configuration lost")
		}
		last := merged.Services[len(merged.Services)-1]
		if last.DependsOn[0] != merged.Services[len(merged.Services)-2].Key {
			t.Fatal("dependency not remapped")
		}
		var dst bytes.Buffer
		if err = WriteMerged(&dst, merged, bytes.NewReader(raw), bytes.NewReader(source), paths); err != nil {
			t.Fatal(err)
		}
		inspected, err := Inspect(bytes.NewReader(dst.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if !SamePackageImages(inspected, merged) {
			t.Fatal("merged archive does not match configuration")
		}
		base, raw = merged, dst.Bytes()
	}
	if len(base.Services) != 5 || len(base.Images) != 3 {
		t.Fatal("successive uploads replaced previous packages")
	}
}

func TestMergeRejectsArchitectureAndLimitsAndDuplicatesIDs(t *testing.T) {
	base, _ := mergeFixture(t, "first")
	added, _ := mergeFixture(t, "first")
	merged, _, err := Merge(base, added)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Services[0].ID == merged.Services[1].ID {
		t.Fatal("duplicate service IDs")
	}
	added.Architecture = "arm64"
	if _, _, err = Merge(base, added); err == nil {
		t.Fatal("mixed architectures accepted")
	}
	added.Architecture = "amd64"
	for i := 0; i < 16; i++ {
		base.Services = append(base.Services, Service{Key: fmt.Sprintf("service-%d", i), Image: "image", CPU: 1, MemoryMB: 128})
	}
	if _, _, err = Merge(base, added); err == nil {
		t.Fatal("service limit ignored")
	}
	base, _ = mergeFixture(t, "first")
	base.Images[0].Key = strings.Repeat("a", 32)
	base.Services[0].Image = base.Images[0].Key
	added = base
	if _, _, err = Merge(base, added); err != nil {
		t.Fatal(err)
	}
}
