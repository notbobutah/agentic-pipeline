package event

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Catalog is the set of event types this deployment may publish. In the full
// design this is the schema registry; for the demo it is demo/catalog.yaml.
type Catalog struct {
	types map[string]bool
}

type catalogFile struct {
	Version string              `yaml:"version"`
	Types   map[string][]string `yaml:"types"` // domain → full type names
}

// LoadCatalog reads a catalog YAML file.
func LoadCatalog(path string) (*Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f catalogFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c := &Catalog{types: map[string]bool{}}
	for _, ts := range f.Types {
		for _, t := range ts {
			c.types[t] = true
		}
	}
	if len(c.types) == 0 {
		return nil, fmt.Errorf("%s: no event types", path)
	}
	return c, nil
}

// Has reports whether t is catalogued.
func (c *Catalog) Has(t string) bool { return c.types[t] }

// Types returns the number of catalogued types.
func (c *Catalog) Len() int { return len(c.types) }
