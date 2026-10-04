package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Template is one CloudFormation template. Nested stacks are loaded as their own Template.
type Template struct {
	StackName  string // deployed stack name; empty for nested stacks and plain files
	Path       string
	Raw        []byte
	Parameters map[string]struct {
		Type string `yaml:"Type"`
	} `yaml:"Parameters"`
	Resources map[string]struct {
		Type string `yaml:"Type"`
	} `yaml:"Resources"`
}

// ParseTemplate parses JSON or YAML; short-form intrinsics (!Ref…) are tolerated
// because only Parameters/Resources types are decoded.
func ParseTemplate(path string, raw []byte) (*Template, error) {
	t := &Template{Path: path, Raw: raw}
	if err := yaml.Unmarshal(raw, t); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return t, nil
}

// LoadInputs loads templates from files and cdk.out directories (default: ./cdk.out),
// including CDK nested stacks and Stages.
func LoadInputs(paths []string) ([]*Template, error) {
	if len(paths) == 0 {
		paths = []string{"cdk.out"}
	}
	var out []*Template
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if fi.IsDir() {
			ts, err := loadAssembly(p)
			if err != nil {
				return nil, err
			}
			out = append(out, ts...)
			continue
		}
		t, err := loadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func loadAssembly(dir string) ([]*Template, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("%s is not a cloud assembly: %w", dir, err)
	}
	var m struct {
		Artifacts map[string]struct {
			Type       string `json:"type"`
			Properties struct {
				TemplateFile  string `json:"templateFile"`
				StackName     string `json:"stackName"`
				DirectoryName string `json:"directoryName"`
			} `json:"properties"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s/manifest.json: %w", dir, err)
	}
	var out []*Template
	for id, a := range m.Artifacts {
		switch a.Type {
		case "aws:cloudformation:stack":
			t, err := loadFile(filepath.Join(dir, a.Properties.TemplateFile))
			if err != nil {
				return nil, err
			}
			t.StackName = a.Properties.StackName
			if t.StackName == "" {
				t.StackName = id
			}
			out = append(out, t)
		case "cdk:cloud-assembly": // Stage
			ts, err := loadAssembly(filepath.Join(dir, a.Properties.DirectoryName))
			if err != nil {
				return nil, err
			}
			out = append(out, ts...)
		}
	}
	// CDK always writes nested stack templates as <id>.nested.template.json; the
	// aws:asset:path metadata pointing at them is only there when the CLI synthesizes.
	nested, err := filepath.Glob(filepath.Join(dir, "*.nested.template.json"))
	if err != nil {
		return nil, err
	}
	for _, p := range nested {
		t, err := loadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// loadFile loads one template.
func loadFile(path string) (*Template, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseTemplate(path, raw)
}
