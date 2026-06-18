package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func loadResources(root string, realRoot string, skillID string) ([]Resource, error) {
	var resources []Resource
	for _, spec := range []struct {
		dir  string
		kind ResourceKind
	}{
		{dir: "references", kind: ResourceKindReference},
		{dir: "assets", kind: ResourceKindAsset},
		{dir: "scripts", kind: ResourceKindScript},
	} {
		dir := filepath.Join(root, spec.dir)
		info, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == dir || entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if entry.Type()&os.ModeSymlink != 0 {
				return appendSymlinkResource(&resources, realRoot, skillID, spec.kind, path, rel)
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if !withinRoot(realRoot, abs) {
				return fmt.Errorf("skill: resource %q escapes skill root", rel)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			resources = append(resources, Resource{
				SkillID: skillID,
				Kind:    spec.kind,
				Path:    rel,
				AbsPath: abs,
				Size:    info.Size(),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Kind == resources[j].Kind {
			return resources[i].Path < resources[j].Path
		}
		return resources[i].Kind < resources[j].Kind
	})
	return resources, nil
}

func appendSymlinkResource(resources *[]Resource, root string, skillID string, kind ResourceKind, path string, rel string) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if !withinRoot(root, target) {
		return fmt.Errorf("skill: resource %q escapes skill root", rel)
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	*resources = append(*resources, Resource{
		SkillID: skillID,
		Kind:    kind,
		Path:    rel,
		AbsPath: target,
		Size:    info.Size(),
	})
	return nil
}

func withinRoot(root string, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}
