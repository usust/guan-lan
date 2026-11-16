package environment

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
)

// Merge appends a validated package, keeping existing keys and remapping all
// incoming references when independently uploaded packages use the same keys.
func Merge(base, added Manifest) (Manifest, map[string]string, error) {
	if base.Architecture != added.Architecture {
		return Manifest{}, nil, fmt.Errorf("追加镜像的架构必须与现有环境一致")
	}
	result := base
	result.Images = append([]Image{}, base.Images...)
	result.Services = append([]Service{}, base.Services...)
	images, services := map[string]bool{}, map[string]bool{}
	for _, im := range base.Images {
		images[im.Key] = true
	}
	for _, s := range base.Services {
		services[s.Key] = true
	}
	unique := func(key string, used map[string]bool) string {
		candidate := key
		for n := 2; used[candidate]; n++ {
			suffix := fmt.Sprintf("-%d", n)
			prefix := key
			if len(prefix)+len(suffix) > 32 {
				prefix = prefix[:32-len(suffix)]
			}
			candidate = prefix + suffix
		}
		used[candidate] = true
		return candidate
	}
	imageKeys, serviceKeys, paths := map[string]string{}, map[string]string{}, map[string]string{}
	usedPaths := map[string]bool{}
	for _, im := range base.Images {
		usedPaths[im.File] = true
	}
	for _, im := range added.Images {
		key := unique(im.Key, images)
		imageKeys[im.Key] = key
		file := "images/" + key + ".tar"
		for n := 2; usedPaths[file]; n++ {
			file = fmt.Sprintf("images/%s-%d.tar", key, n)
		}
		usedPaths[file] = true
		paths[im.File] = file
		im.Key = key
		im.File = file
		result.Images = append(result.Images, im)
	}
	for _, s := range added.Services {
		serviceKeys[s.Key] = unique(s.Key, services)
	}
	ids := map[string]bool{}
	for _, s := range base.Services {
		ids[s.ID] = true
	}
	for _, s := range added.Services {
		if s.ID != "" && ids[s.ID] {
			sum := sha256.Sum256([]byte(s.ID + "\x00" + serviceKeys[s.Key]))
			s.ID = "svc-" + hex.EncodeToString(sum[:16])
		}
		ids[s.ID] = true
		if s.Name == "" {
			s.Name = s.Key
		}
		s.Key = serviceKeys[s.Key]
		s.Image = imageKeys[s.Image]
		s.DependsOn = append([]string{}, s.DependsOn...)
		for i, dep := range s.DependsOn {
			s.DependsOn[i] = serviceKeys[dep]
		}
		result.Services = append(result.Services, s)
	}
	return result, paths, result.Validate()
}

// WriteMerged streams only manifest-declared image members into one package.
func WriteMerged(dst io.Writer, manifest Manifest, base, added io.Reader, addedPaths map[string]string) error {
	tw := tar.NewWriter(dst)
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}
	if err = tw.WriteHeader(&tar.Header{Name: "manifest.yaml", Mode: 0600, Size: int64(len(data))}); err != nil {
		return err
	}
	if _, err = tw.Write(data); err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, im := range manifest.Images {
		wanted[im.File] = true
	}
	seen := map[string]bool{}
	for i, src := range []io.Reader{base, added} {
		tr := tar.NewReader(src)
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			name := h.Name
			if i == 1 {
				name = addedPaths[name]
			}
			if !wanted[name] {
				continue
			}
			if seen[name] || h.Typeflag != tar.TypeReg {
				return fmt.Errorf("合并镜像归档成员无效或重复")
			}
			seen[name] = true
			if e = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: h.Size}); e != nil {
				return e
			}
			if _, e = io.Copy(tw, tr); e != nil {
				return e
			}
		}
	}
	if len(seen) != len(wanted) {
		return fmt.Errorf("合并环境包缺少镜像")
	}
	return tw.Close()
}
