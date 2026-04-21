// Package sync implements the bidirectional synchronisation logic between
// Google Contacts and local Markdown files.
package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gcontacts-sync/internal/contacts"
	"gcontacts-sync/internal/markdown"
	"gcontacts-sync/internal/model"
)

// Options configures the sync behaviour.
type Options struct {
	DryRun  bool
	Verbose bool
	// UpdateDelay is the pause between successive write API calls to avoid
	// hitting Google's "critical write requests per minute" quota.
	// Defaults to 500ms if zero.
	UpdateDelay time.Duration
	// DeleteOrphans removes local .md files whose resource_name no longer
	// exists in Google (i.e. the contact was deleted on the server).
	// When false (default) orphaned files are reported as warnings but kept.
	DeleteOrphans bool
}

// Syncer orchestrates the sync between Google Contacts and the Markdown store.
type Syncer struct {
	google *contacts.Service
	store  *markdown.Store
	opts   Options
}

// NewSyncer creates a new Syncer.
func NewSyncer(google *contacts.Service, store *markdown.Store, opts Options) *Syncer {
	if opts.UpdateDelay == 0 {
		opts.UpdateDelay = 500 * time.Millisecond
	}
	return &Syncer{google: google, store: store, opts: opts}
}

// throttle pauses between write API calls unless DryRun is active.
func (s *Syncer) throttle(ctx context.Context) {
	if s.opts.DryRun {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(s.opts.UpdateDelay):
	}
}

// Pull fetches all Google Contacts and writes/updates Markdown files.
// Local files whose resource_name no longer exists in Google are deleted
// (if DeleteOrphans is set) or reported as warnings.
func (s *Syncer) Pull(ctx context.Context) (*Report, error) {
	r := &Report{Mode: "pull"}

	remote, err := s.google.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	// Index remote by resource name so we can detect orphans.
	remoteByResource := make(map[string]struct{}, len(remote))
	for _, c := range remote {
		remoteByResource[c.ResourceName] = struct{}{}
	}

	// Track paths written during this pull so filename collisions don't get
	// misreported as updates (two contacts with the same sanitised name would
	// otherwise hit the else-branch on the second iteration).
	writtenThisRun := make(map[string]struct{}, len(remote))

	for _, c := range remote {
		path := s.store.Path(c)

		// If this path was already written this run (filename collision), make
		// it unique by appending the short numeric suffix of the resource name.
		if _, collision := writtenThisRun[path]; collision {
			path = deduplicatePath(path, c.ResourceName)
		}

		existing, readErr := s.store.Read(path)

		if readErr != nil || isWrittenThisRun(writtenThisRun, path) {
			// New file (or collision-deduplicated path)
			if !s.opts.DryRun {
				if err := os.WriteFile(path, mustMarshal(c), 0o644); err != nil {
					r.Errors = append(r.Errors, fmt.Errorf("writing %s: %w", path, err))
					continue
				}
			}
			writtenThisRun[path] = struct{}{}
			r.Created = append(r.Created, c.Key())
			s.logf("CREATE %s", path)
		} else {
			// File exists - update only if Google's version is newer
			if c.UpdatedAt.Truncate(time.Second).After(existing.UpdatedAt.Truncate(time.Second)) {
				if c.Notes == "" && existing.Notes != "" {
					c.Notes = existing.Notes
				}
				if !s.opts.DryRun {
					if err := os.WriteFile(path, mustMarshal(c), 0o644); err != nil {
						r.Errors = append(r.Errors, fmt.Errorf("updating %s: %w", path, err))
						continue
					}
				}
				writtenThisRun[path] = struct{}{}
				r.Updated = append(r.Updated, c.Key())
				s.logf("UPDATE %s", path)
			} else {
				writtenThisRun[path] = struct{}{}
				r.Unchanged = append(r.Unchanged, c.Key())
				s.logf("SKIP   %s (unchanged)", path)
			}
		}
	}

	// Orphan sweep: local files with a resource_name Google no longer knows about.
	if err := s.sweepOrphans(ctx, remoteByResource, r); err != nil {
		return nil, err
	}

	return r, nil
}

// Push reads all Markdown files and creates/updates Google Contacts accordingly.
func (s *Syncer) Push(ctx context.Context) (*Report, error) {
	r := &Report{Mode: "push"}

	// Fetch the current ETag index from Google so we never need a per-contact Get.
	etagCache, err := s.buildETagCache(ctx)
	if err != nil {
		return nil, err
	}

	local, err := s.store.ReadAll()
	if err != nil {
		return nil, err
	}

	for _, c := range local {
		if c.ResourceName == "" {
			// New contact - create it
			if !s.opts.DryRun {
				created, err := s.google.Create(ctx, c)
				if err != nil {
					r.Errors = append(r.Errors, fmt.Errorf("creating %q: %w", c.Key(), err))
					continue
				}
				created.Notes = c.Notes
				if err := s.store.Write(created); err != nil {
					r.Errors = append(r.Errors, fmt.Errorf("writing back %q: %w", c.Key(), err))
				}
				s.throttle(ctx)
			}
			r.Created = append(r.Created, c.Key())
			s.logf("CREATE %q in Google", c.Key())
		} else {
			// Existing contact - update it using the cached ETag
			freshETag := etagCache[c.ResourceName]
			if !s.opts.DryRun {
				if _, err := s.google.UpdateWithCache(ctx, c, freshETag); err != nil {
					if errors.Is(err, contacts.ErrNotFound) {
						c.ResourceName = ""
						c.ETag = ""
						if writeErr := s.store.Write(c); writeErr != nil {
							r.Errors = append(r.Errors, fmt.Errorf("clearing resource_name for %q: %w", c.Key(), writeErr))
						}
						r.Warnings = append(r.Warnings, c.Key()+" - not found on server (merged?), cleared resource_name")
						s.logf("WARN   %q not found on server, resource_name cleared", c.Key())
						continue
					}
					r.Errors = append(r.Errors, fmt.Errorf("updating %q: %w", c.Key(), err))
					continue
				}
				s.throttle(ctx)
			}
			r.Updated = append(r.Updated, c.Key())
			s.logf("UPDATE %q in Google", c.Key())
		}
	}

	return r, nil
}

// Sync performs a bidirectional sync using last-modified timestamps to resolve conflicts.
// Google Contact wins on conflict (last-write wins per source).
func (s *Syncer) Sync(ctx context.Context) (*Report, error) {
	r := &Report{Mode: "sync"}

	remote, err := s.google.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	// Index remote by resource name; also build ETag cache inline.
	remoteByResource := make(map[string]struct{}, len(remote))
	etagCache := make(map[string]string, len(remote))
	for _, c := range remote {
		remoteByResource[c.ResourceName] = struct{}{}
		etagCache[c.ResourceName] = c.ETag
	}

	local, err := s.store.ReadAll()
	if err != nil {
		return nil, err
	}

	localByResource := make(map[string]*model.Contact, len(local))
	for _, c := range local {
		if c.ResourceName != "" {
			localByResource[c.ResourceName] = c
		}
	}

	// --- Pull: remote -> local ---
	for _, rc := range remote {
		lc, exists := localByResource[rc.ResourceName]
		if !exists {
			if !s.opts.DryRun {
				if err := s.store.Write(rc); err != nil {
					r.Errors = append(r.Errors, err)
					continue
				}
			}
			r.Created = append(r.Created, rc.Key()+" (-> local)")
			s.logf("CREATE local %s", markdown.Filename(rc))
			continue
		}

		localMod := markdown.IsLocallyModified(lc)
		googleNewer := rc.UpdatedAt.Truncate(time.Second).After(lc.UpdatedAt.Truncate(time.Second))

		switch {
		case googleNewer && !localMod:
			// Google changed, local untouched — pull down.
			rc.Notes = mergeNotes(rc.Notes, lc.Notes)
			if !s.opts.DryRun {
				if err := s.store.Write(rc); err != nil {
					r.Errors = append(r.Errors, err)
					continue
				}
			}
			r.Updated = append(r.Updated, rc.Key()+" (Google -> local)")
			s.logf("UPDATE local %s (Google newer)", markdown.Filename(rc))

		case localMod && !googleNewer:
			// Local edited, Google unchanged — push up.
			freshETag := etagCache[lc.ResourceName]
			if !s.opts.DryRun {
				if _, err := s.google.UpdateWithCache(ctx, lc, freshETag); err != nil {
					if errors.Is(err, contacts.ErrNotFound) {
						r.Warnings = append(r.Warnings, lc.Key()+" - not found on server (merged?), will re-pull on next run")
						s.logf("WARN   %q not found on server during push, skipping", lc.Key())
						continue
					}
					r.Errors = append(r.Errors, err)
					continue
				}
				s.throttle(ctx)
			}
			r.Updated = append(r.Updated, lc.Key()+" (local -> Google)")
			s.logf("UPDATE Google %q (local edited)", lc.Key())

		case googleNewer && localMod:
			// Both changed — Google wins; warn the user.
			rc.Notes = mergeNotes(rc.Notes, lc.Notes)
			if !s.opts.DryRun {
				if err := s.store.Write(rc); err != nil {
					r.Errors = append(r.Errors, err)
					continue
				}
			}
			r.Updated = append(r.Updated, rc.Key()+" (Google -> local, conflict)")
			r.Warnings = append(r.Warnings, lc.Key()+" - edited both locally and in Google; Google version kept")
			s.logf("CONFLICT %s (Google wins)", markdown.Filename(rc))

		default:
			// Neither changed.
			r.Unchanged = append(r.Unchanged, rc.Key())
			s.logf("SKIP   %s", markdown.Filename(rc))
		}
	}

	// --- Push: local-only contacts to Google ---
	for _, lc := range local {
		if lc.ResourceName == "" {
			if !s.opts.DryRun {
				created, err := s.google.Create(ctx, lc)
				if err != nil {
					r.Errors = append(r.Errors, err)
					continue
				}
				created.Notes = lc.Notes
				if err := s.store.Write(created); err != nil {
					r.Errors = append(r.Errors, err)
				}
				s.throttle(ctx)
			}
			r.Created = append(r.Created, lc.Key()+" (-> Google)")
			s.logf("CREATE Google %q", lc.Key())
		}
	}

	// Orphan sweep: local files with a resource_name Google no longer knows about.
	if err := s.sweepOrphans(ctx, remoteByResource, r); err != nil {
		return nil, err
	}

	return r, nil
}

// buildETagCache does a single ListAll and returns a map of resourceName -> ETag.
// This is used by Push (which does not otherwise call ListAll) so we have fresh
// ETags without any per-contact Get calls.
func (s *Syncer) buildETagCache(ctx context.Context) (map[string]string, error) {
	remote, err := s.google.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("building ETag cache: %w", err)
	}
	cache := make(map[string]string, len(remote))
	for _, c := range remote {
		cache[c.ResourceName] = c.ETag
	}
	return cache, nil
}

func mergeNotes(googleNotes, localNotes string) string {
	if googleNotes != "" {
		return googleNotes
	}
	return localNotes
}

// deduplicatePath appends the last numeric segment of a resource name to a
// path stem to resolve filename collisions, e.g.
// "contacts/John-Smith.md" + "people/c9876" -> "contacts/John-Smith-9876.md"
func deduplicatePath(path, resourceName string) string {
	// Extract trailing digits from the resource name.
	suffix := resourceName
	if idx := strings.LastIndexAny(resourceName, "/c"); idx >= 0 {
		suffix = resourceName[idx+1:]
	}
	if suffix == "" || suffix == resourceName {
		suffix = strings.ReplaceAll(resourceName, "/", "-")
	}
	// Insert before the .md extension.
	if strings.HasSuffix(path, ".md") {
		return path[:len(path)-3] + "-" + suffix + ".md"
	}
	return path + "-" + suffix
}

func isWrittenThisRun(m map[string]struct{}, path string) bool {
	_, ok := m[path]
	return ok
}

// mustMarshal marshals a contact to bytes, returning nil on error (the caller
// checks the write error separately).
func mustMarshal(c *model.Contact) []byte {
	b, err := markdown.Marshal(c)
	if err != nil {
		return nil
	}
	return b
}

func (s *Syncer) logf(format string, args ...any) {
	if s.opts.Verbose {
		fmt.Printf(format+"\n", args...)
	}
}

// sweepOrphans scans all local .md files and removes (or warns about) any whose
// resource_name is not present in remoteByResource.
// Files with no resource_name at all are local-only new contacts and are skipped.
func (s *Syncer) sweepOrphans(ctx context.Context, remoteByResource map[string]struct{}, r *Report) error {
	local, err := s.store.ReadAll()
	if err != nil {
		return fmt.Errorf("sweepOrphans reading store: %w", err)
	}

	for _, lc := range local {
		if lc.ResourceName == "" {
			// No resource_name means it was never synced to Google — not an orphan.
			continue
		}
		if _, exists := remoteByResource[lc.ResourceName]; exists {
			continue
		}

		// This contact is gone from Google.
		path := s.store.Path(lc)
		if s.opts.DeleteOrphans {
			if !s.opts.DryRun {
				if err := os.Remove(path); err != nil {
					r.Errors = append(r.Errors, fmt.Errorf("deleting orphan %q: %w", path, err))
					continue
				}
			}
			r.Deleted = append(r.Deleted, lc.Key())
			s.logf("DELETE %s (deleted on Google)", path)
		} else {
			r.Warnings = append(r.Warnings, lc.Key()+" - deleted on Google; run with --delete-orphans to remove local file")
			s.logf("WARN   %s deleted on Google but kept locally (use --delete-orphans)", path)
		}
	}
	return nil
}
