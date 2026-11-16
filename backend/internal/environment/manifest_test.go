package environment

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func sample() Manifest {
	return Manifest{FormatVersion: 1, Version: "1.0", Architecture: "amd64", Images: []Image{{Key: "base", File: "images/base.tar", SHA256: strings.Repeat("0", 64)}}, Services: []Service{{Key: "web", Image: "base", MemoryMB: 128, CPU: 1, Ports: []Port{{Port: 80, Protocol: "tcp", Public: true}}}, {Key: "admin", Image: "base", MemoryMB: 128, CPU: 1, DependsOn: []string{"web"}, Ports: []Port{{Port: 80, Protocol: "tcp", Public: true}}}}}
}
func TestManifestValidation(t *testing.T) {
	m := sample()
	if err := m.Validate(); err != nil {
		t.Fatal("same ports on different containers must work", err)
	}
	cases := map[string]func(*Manifest){
		"cycle":              func(m *Manifest) { m.Services[0].DependsOn = []string{"admin"} },
		"missing dependency": func(m *Manifest) { m.Services[0].DependsOn = []string{"missing"} },
		"duplicate service":  func(m *Manifest) { m.Services[1].Key = "web" },
		"invalid image":      func(m *Manifest) { m.Services[0].Image = "none" },
		"path traversal":     func(m *Manifest) { m.Images[0].File = "images/../secret.tar" },
		"duplicate port":     func(m *Manifest) { m.Services[0].Ports = append(m.Services[0].Ports, m.Services[0].Ports[0]) },
		"invalid protocol":   func(m *Manifest) { m.Services[0].Ports[0].Protocol = "sctp" },
		"negative resource":  func(m *Manifest) { m.Services[0].CPU = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := sample()
			mutate(&m)
			if m.Validate() == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
	data, _ := yaml.Marshal(sample())
	if _, err := Parse(append(data, []byte("privileged: true\n")...)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := Parse(append(data, []byte("---\n{}\n")...)); err == nil {
		t.Fatal("extra document accepted")
	}
	for _, bad := range []string{".nan", ".inf", "-.inf"} {
		text := strings.Replace(string(data), "cpu: 1", "cpu: "+bad, 1)
		if _, err := Parse([]byte(text)); err == nil {
			t.Fatal("non-finite cpu accepted", bad)
		}
	}
}

type member struct {
	name string
	data []byte
	kind byte
}

func archive(t *testing.T, members ...member) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, m := range members {
		kind := m.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		h := &tar.Header{Name: m.name, Mode: 0600, Size: int64(len(m.data)), Typeflag: kind}
		if kind == tar.TypeSymlink {
			h.Linkname = "/etc/passwd"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(m.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestArchiveContractAndDangerousMembers(t *testing.T) {
	image := []byte("nested image archive bytes")
	m := sample()
	m.Images[0].SHA256 = fmt.Sprintf("%x", sha256.Sum256(image))
	data, _ := yaml.Marshal(m)
	good := []member{{"manifest.yaml", data, 0}, {"images/base.tar", image, 0}}
	if got, err := Inspect(bytes.NewReader(archive(t, good...))); err != nil || len(got.Services) != 2 {
		t.Fatal(got, err)
	}
	cases := map[string][]member{
		"missing manifest": {{"images/base.tar", image, 0}},
		"missing image":    {{"manifest.yaml", data, 0}},
		"wrong digest":     {{"manifest.yaml", data, 0}, {"images/base.tar", []byte("different"), 0}},
		"traversal":        append(append([]member{}, good...), member{"../escape", nil, 0}),
		"absolute":         append(append([]member{}, good...), member{"/etc/passwd", nil, 0}),
		"link":             append(append([]member{}, good...), member{"images/link.tar", nil, tar.TypeSymlink}),
		"duplicate":        append(append([]member{}, good...), good[0]),
		"extra image":      append(append([]member{}, good...), member{"images/extra.tar", image, 0}),
		"host script":      append(append([]member{}, good...), member{"setup.sh", []byte("script"), 0}),
	}
	for name, members := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Inspect(bytes.NewReader(archive(t, members...))); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	dataArchive := archive(t, good...)
	if _, err := Inspect(bytes.NewReader(append(append([]byte{}, dataArchive...), []byte("hidden data")...))); err == nil {
		t.Fatal("trailing payload accepted")
	}
	if _, err := Inspect(bytes.NewReader(dataArchive[:600])); err == nil {
		t.Fatal("truncated archive accepted")
	}
}

// Generate a large nested archive as a stream rather than allocating 513 MiB.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestArchiveBeyondFormerSizeLimit(t *testing.T) {
	const size = 513 << 20
	hash := sha256.New()
	if _, err := io.CopyN(hash, zeroReader{}, size); err != nil {
		t.Fatal(err)
	}
	m := sample()
	m.Images[0].SHA256 = fmt.Sprintf("%x", hash.Sum(nil))
	data, _ := yaml.Marshal(m)
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		tw := tar.NewWriter(writer)
		err := tw.WriteHeader(&tar.Header{Name: "manifest.yaml", Mode: 0600, Size: int64(len(data))})
		if err == nil {
			_, err = tw.Write(data)
		}
		if err == nil {
			err = tw.WriteHeader(&tar.Header{Name: "images/base.tar", Mode: 0600, Size: size})
		}
		if err == nil {
			_, err = io.CopyN(tw, zeroReader{}, size)
		}
		if err == nil {
			err = tw.Close()
		}
		_ = writer.CloseWithError(err)
		done <- err
	}()
	_, err := Inspect(reader)
	_ = reader.CloseWithError(err)
	writeErr := <-done
	if err != nil {
		t.Fatal(err)
	}
	if writeErr != nil {
		t.Fatal(writeErr)
	}
}

func TestOptionalResourceLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		memory  int
		cpu     float64
		pids    int64
		invalid bool
	}{
		{"unlimited", 0, 0, 0, false},
		{"custom", 1024, 2.5, 512, false},
		{"small memory", 1, 0, 0, true},
		{"negative cpu", 0, -1, 0, true},
		{"negative pids", 0, 0, -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sample()
			m.Services[0].ResourceLimitsEnabled = true
			m.Services[0].MemoryMB, m.Services[0].CPU, m.Services[0].PidsLimit = tc.memory, tc.cpu, tc.pids
			if err := m.Validate(); (err != nil) != tc.invalid {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}
