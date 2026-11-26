package server

import (
	"fmt"
	"slices"
	"sort"
)

// catalog is what the server serves at one time: its packages (Config.Packages, then the bundles in
// the order it read them), their kinds by name, each with its versions from the lowest, the kinds in
// the order they came, the operations and their routes, and the layout of the organization the
// kinds read. A catalog does not change: a bundle the server reads makes a new one (Server.add),
// and closes changed of the one it replaces, so that what keeps something made of the packages (the
// pages, the agents' tools) knows to make it again.
type catalog struct {
	packages []*Package
	kinds    map[string][]*kind
	order    []string
	ops      map[string]*operation
	routes   []*operation
	layout   OrgLayout
	changed  chan struct{}
}

// newCatalog makes the catalog of the packages: it refuses a version of a kind in two packages
// that describe it otherwise, and kinds that read two organizations. A version two packages describe
// alike (a bundle holds the versions of its project the server has: DESIGN 15.1) is served by the
// first of them.
func newCatalog(packages []*Package) (*catalog, error) {
	c := &catalog{packages: packages, kinds: map[string][]*kind{}, ops: map[string]*operation{}, changed: make(chan struct{})}
	masters := map[string]MasterLayout{}
	for _, p := range packages {
	kinds:
		for _, k := range p.kinds {
			for _, have := range c.kinds[k.Name] {
				if have.Version == k.Version {
					if sameDescription(have, p.files) {
						continue kinds
					}
					return nil, fmt.Errorf("the kind %s version %d is in two packages", k.Name, k.Version)
				}
			}
			if len(c.kinds[k.Name]) == 0 {
				c.order = append(c.order, k.Name)
			}
			c.kinds[k.Name] = append(c.kinds[k.Name], k)
			o := k.desc.Organization
			switch {
			case c.layout.Data == "":
				c.layout.Name, c.layout.Data = o.Name, o.Data
				c.layout.Written = OrgWritten{Positions: map[string]string{}, TeamKinds: map[string]string{}}
				for _, x := range o.Positions {
					c.layout.Positions = append(c.layout.Positions, x.Name)
					if x.Written != "" && x.Written != x.Name {
						c.layout.Written.Positions[x.Written] = x.Name
					}
				}
				for _, x := range o.TeamKinds {
					c.layout.TeamKinds = append(c.layout.TeamKinds, x.Name)
					if x.Written != "" && x.Written != x.Name {
						c.layout.Written.TeamKinds[x.Written] = x.Name
					}
				}
			case c.layout.Data != o.Data:
				return nil, fmt.Errorf("the kinds read two organizations (%s and %s); a server serves one", c.layout.Data, o.Data)
			}
			for _, m := range k.desc.Masters {
				ml := MasterLayout{Name: m.Masters + "." + m.Master, Data: m.Data, Key: m.Key, NameField: m.NameField}
				for _, f := range m.Fields {
					ml.Fields = append(ml.Fields, f.Name)
					if f.Optional && f.Name != m.Key {
						if ml.Optional == nil {
							ml.Optional = map[string]bool{}
						}
						ml.Optional[f.Name] = true
					}
					if f.Written != "" && f.Written != f.Name {
						if ml.Written == nil {
							ml.Written = map[string]string{}
						}
						ml.Written[f.Name] = f.Written
					}
				}
				masters[ml.Name] = ml
			}
		}
		for id, op := range p.api.ops {
			c.ops[id] = op
		}
	}
	for _, ks := range c.kinds {
		sort.Slice(ks, func(i, j int) bool { return ks[i].Version < ks[j].Version })
	}
	for _, ml := range masters {
		c.layout.Masters = append(c.layout.Masters, ml)
	}
	sort.Slice(c.layout.Masters, func(i, j int) bool { return c.layout.Masters[i].Name < c.layout.Masters[j].Name })
	for _, op := range c.ops {
		c.routes = append(c.routes, op)
	}
	sort.Slice(c.routes, func(i, j int) bool { return c.routes[i].id < c.routes[j].id })
	return c, nil
}

// cat answers the catalog the server serves now. A reader loads it once for what it reads together.
func (s *Server) cat() *catalog { return s.cur.Load() }

// with answers the catalog of the server's packages and p besides, or why p does not go with them:
// a version of a kind the server has, or another organization (newCatalog), or an organization
// declared otherwise than the server's (sameOrganization).
func (c *catalog) with(p *Package) (*catalog, error) {
	if err := c.sameOrganization(p); err != nil {
		return nil, err
	}
	return newCatalog(append(slices.Clone(c.packages), p))
}

// sameOrganization says why the kinds of p read the organization otherwise than the server's kinds:
// another directory of its data, or other positions or kinds of team, which the server checks the
// data with (orgcheck.go). nil when they read it alike, and when the server has no kind yet.
func (c *catalog) sameOrganization(p *Package) error {
	if c.layout.Data == "" {
		return nil
	}
	for _, k := range p.kinds {
		o := k.desc.Organization
		var positions, teamKinds []string
		for _, x := range o.Positions {
			positions = append(positions, x.Name)
		}
		for _, x := range o.TeamKinds {
			teamKinds = append(teamKinds, x.Name)
		}
		switch {
		case o.Data != c.layout.Data:
			return fmt.Errorf("the kind %s reads the organization's data in %s, and the server's kinds in %s; a server serves one organization", k.Name, o.Data, c.layout.Data)
		case !slices.Equal(positions, c.layout.Positions):
			return fmt.Errorf("the kind %s declares the positions %v, and the server's kinds %v; generate it from the same organization", k.Name, positions, c.layout.Positions)
		case !slices.Equal(teamKinds, c.layout.TeamKinds):
			return fmt.Errorf("the kind %s declares the kinds of team %v, and the server's kinds %v; generate it from the same organization", k.Name, teamKinds, c.layout.TeamKinds)
		}
	}
	return nil
}
