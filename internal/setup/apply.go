package setup

import (
	"fmt"

	"github.com/Snarap1/hqdb/internal/secrets"
)

// Item pairs one found Secret with the Store chosen for it.
type Item struct {
	Secret Secret
	Store  Store
}

// Result reports one applied item.
type Result struct {
	// Profile is the Secret's Profile name.
	Profile string
	// Kind is the Secret's kind.
	Kind SecretKind
	// Ok is true when the secret is stored and the config references it.
	Ok bool
	// Err is the failure when Ok is false; it never contains the secret.
	Err error
	// Notes is the wiring the user must do outside hqdb (the env store).
	Notes []string
	// Reference is the option value the Profile now carries.
	Reference string
}

// Apply moves every item's secret into its store, then patches the
// config. The store is proven first: its read-back (Verify) must return
// the same secret before the config file is touched, so a store that
// did not take the value never strands the config pointing at it. A
// failed item is reported and skipped; the other items still apply.
func Apply(items []Item, run Runner) []Result {
	var out []Result
	for _, it := range items {
		r := Result{Profile: it.Secret.Profile, Kind: it.Secret.Kind, Reference: it.Store.Reference(it.Secret.Profile)}
		if !secrets.HasReference(r.Reference) {
			r.Err = fmt.Errorf("store %q did not produce a reference form (got a literal); nothing was changed", it.Store.ID)
			out = append(out, r)
			continue
		}
		notes, err := it.Store.Move(run, it.Secret.Profile, it.Secret.Value)
		if err != nil {
			r.Err = fmt.Errorf("storing in %s failed: %v", it.Store.Title, err)
			out = append(out, r)
			continue
		}
		r.Notes = notes
		if it.Store.Verify != nil {
			if err := it.Store.Verify(run, it.Secret.Profile, it.Secret.Value); err != nil {
				r.Err = fmt.Errorf("%s did not read the secret back: %v", it.Store.Title, err)
				out = append(out, r)
				continue
			}
		}
		if err := Patch(it.Secret.File, it.Secret.Profile, it.Secret.Kind, it.Secret.Value, r.Reference); err != nil {
			r.Err = fmt.Errorf("stored in %s, but the config was not rewritten: %v", it.Store.Title, err)
			out = append(out, r)
			continue
		}
		r.Ok = true
		out = append(out, r)
	}
	return out
}
