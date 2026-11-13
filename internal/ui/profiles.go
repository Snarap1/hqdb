package ui

import "github.com/Snarap1/hqdb/internal/config"

// profileNodes builds one kindProfile root node per Profile of cfg, sorted
// by name. A Profile that fails config.Validate is still included, marked
// broken with its full error, so one bad Profile never hides the rest.
// Building these nodes never opens a connection.
func profileNodes(cfg *config.Config) []*node {
	names := config.SortedProfileNames(cfg)
	nodes := make([]*node, 0, len(names))
	for _, name := range names {
		p := cfg.Profiles[name]
		access, _ := config.ProfileAccess(p) // an invalid access fails Validate below
		adapter, err := config.Validate(p)
		if err != nil {
			nodes = append(nodes, &node{kind: kindProfile, name: name, profile: name,
				adapter: config.AdapterName(p), validateErr: err, access: access})
			continue
		}
		nodes = append(nodes, &node{kind: kindProfile, name: name, profile: name, adapter: adapter, access: access})
	}
	return nodes
}
