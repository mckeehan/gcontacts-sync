package sync

import "fmt"

// Report summarises the result of a sync operation.
type Report struct {
	Mode      string
	Created   []string
	Updated   []string
	Unchanged []string
	Deleted   []string
	Warnings  []string
	Errors    []error
}

// Print outputs a human-readable summary to stdout.
func (r *Report) Print() {
	fmt.Printf("\n── Sync report (%s) ──────────────────────\n", r.Mode)
	fmt.Printf("  Created:   %d\n", len(r.Created))
	fmt.Printf("  Updated:   %d\n", len(r.Updated))
	fmt.Printf("  Unchanged: %d\n", len(r.Unchanged))
	fmt.Printf("  Deleted:   %d\n", len(r.Deleted))
	fmt.Printf("  Warnings:  %d\n", len(r.Warnings))
	fmt.Printf("  Errors:    %d\n", len(r.Errors))

	if len(r.Warnings) > 0 {
		fmt.Println("\n  Warnings:")
		for _, w := range r.Warnings {
			fmt.Printf("    ⚠  %s\n", w)
		}
	}
	if len(r.Errors) > 0 {
		fmt.Println("\n  Errors:")
		for _, e := range r.Errors {
			fmt.Printf("    • %v\n", e)
		}
	}
	fmt.Println()
}
