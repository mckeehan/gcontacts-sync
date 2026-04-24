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
	// Only files that have a synced_hash (previously pulled from Google) are
	// deleted. Files without a synced_hash have their IDs cleared instead.
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

func (s *Syncer) throttle(ctx context.Context) {
	if s.opts.DryRun {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(s.opts.UpdateDelay):
	}
}

// createAndWriteBack creates a contact in Google, fetches it back fully, and
// writes the fetched record to the original local file path. This guarantees
// the local file always ends up with the canonical Google data including
// resource_name, etag, display_name, synced_hash, and updated_at.
func (s *Syncer) createAndWriteBack(ctx context.Context, lc *model.Contact, r *Report) bool {
	created, err := s.google.Create(ctx, lc)
	if err != nil {
		r.Errors = append(r.Errors, fmt.Errorf("creating %q in Google: %w", lc.Key(), err))
		return false
	}
	s.logf("  created in Google: resource_name=%s", created.ResourceName)

	// Fetch back so we have all fields Google populates (display_name, groups, etc).
	fetched, err := s.google.Get(ctx, created.ResourceName)
	if err != nil {
		r.Errors = append(r.Errors, fmt.Errorf("fetching new contact %q from Google: %w", lc.Key(), err))
		return false
	}
	s.logf("  fetched from Google: display_name=%q resource_name=%s", fetched.DisplayName, fetched.ResourceName)

	// Preserve local notes if Google didn't store them.
	if fetched.Notes == "" {
		fetched.Notes = lc.Notes
	}

	// Write to the original file path (lc.FilePath set by ReadAll).
	// This is critical: we must NOT use store.Path(fetched) because that
	// derives the path from fetched.DisplayName which may differ from the
	// original filename, leaving the old file untouched with no resource_name.
	if lc.FilePath == "" {
		r.Errors = append(r.Errors, fmt.Errorf("contact %q has no FilePath — cannot write back", lc.Key()))
		return false
	}
	if err := s.store.WriteToPath(lc.FilePath, fetched); err != nil {
		r.Errors = append(r.Errors, fmt.Errorf("writing back %q to %s: %w", lc.Key(), lc.FilePath, err))
		return false
	}
	s.logf("  wrote back to %s (resource_name=%s synced_hash set)", lc.FilePath, fetched.ResourceName)
	return true
}

// Pull fetches all Google Contacts and writes/updates Markdown files.
func (s *Syncer) Pull(ctx context.Context) (*Report, error) {
	r := &Report{Mode: "pull"}

	remote, err := s.google.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	remoteByResource := make(map[string]struct{}, len(remote))
	for _, c := range remote {
		remoteByResource[c.ResourceName] = struct{}{}
	}

	writtenThisRun := make(map[string]struct{}, len(remote))

	for _, c := range remote {
		path := s.store.Path(c)
		if _, collision := writtenThisRun[path]; collision {
			path = deduplicatePath(path, c.ResourceName)
		}

		existing, readErr := s.store.Read(path)
		if readErr != nil {
			if !s.opts.DryRun {
				if err := s.store.WriteToPath(path, c); err != nil {
					r.Errors = append(r.Errors, fmt.Errorf("writing %s: %w", path, err))
					continue
				}
			}
			writtenThisRun[path] = struct{}{}
			r.Created = append(r.Created, c.Key())
			s.logf("CREATE %s", path)
		} else {
			if c.UpdatedAt.Truncate(time.Second).After(existing.UpdatedAt.Truncate(time.Second)) {
				if c.Notes == "" && existing.Notes != "" {
					c.Notes = existing.Notes
				}
				if !s.opts.DryRun {
					if err := s.store.WriteToPath(path, c); err != nil {
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

	if err := s.sweepOrphans(remoteByResource, r); err != nil {
		return nil, err
	}
	return r, nil
}

// Push reads all Markdown files and creates/updates Google Contacts.
func (s *Syncer) Push(ctx context.Context) (*Report, error) {
	r := &Report{Mode: "push"}

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
			s.logf("CREATE %q -> Google (FilePath=%s)", c.Key(), c.FilePath)
			if !s.opts.DryRun {
				if ok := s.createAndWriteBack(ctx, c, r); !ok {
					continue
				}
				s.throttle(ctx)
			}
			r.Created = append(r.Created, c.Key())
		} else {
			freshETag := etagCache[c.ResourceName]
			if !s.opts.DryRun {
				if _, err := s.google.UpdateWithCache(ctx, c, freshETag); err != nil {
					if errors.Is(err, contacts.ErrNotFound) {
						c.ResourceName = ""
						c.ETag = ""
						if err := s.store.WriteToPath(c.FilePath, c); err != nil {
							r.Errors = append(r.Errors, fmt.Errorf("clearing resource_name for %q: %w", c.Key(), err))
						}
						r.Warnings = append(r.Warnings, c.Key()+" - not found on server, cleared resource_name")
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

// Sync performs a bidirectional sync.
func (s *Syncer) Sync(ctx context.Context) (*Report, error) {
	r := &Report{Mode: "sync"}

	remote, err := s.google.ListAll(ctx)
	if err != nil {
		return nil, err
	}

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

	writtenPaths := make(map[string]struct{})

	// --- Pull pass: remote -> local ---
	for _, rc := range remote {
		lc, exists := localByResource[rc.ResourceName]
		if !exists {
			path := s.store.Path(rc)
			if _, alreadyWritten := writtenPaths[path]; alreadyWritten {
				s.logf("SKIP   %s (duplicate in Google, run --dedup to clean up)", path)
				continue
			}
			if !s.opts.DryRun {
				if err := s.store.WriteToPath(path, rc); err != nil {
					r.Errors = append(r.Errors, err)
					continue
				}
				writtenPaths[path] = struct{}{}
				remoteByResource[rc.ResourceName] = struct{}{}
			}
			r.Created = append(r.Created, rc.Key()+" (-> local)")
			s.logf("CREATE local %s", markdown.Filename(rc))
			continue
		}

		localMod := markdown.IsLocallyModified(lc)
		googleNewer := rc.UpdatedAt.Truncate(time.Second).After(lc.UpdatedAt.Truncate(time.Second))

		switch {
		case googleNewer && !localMod:
			rc.Notes = mergeNotes(rc.Notes, lc.Notes)
			if !s.opts.DryRun {
				if err := s.store.WriteToPath(lc.FilePath, rc); err != nil {
					r.Errors = append(r.Errors, err)
					continue
				}
			}
			r.Updated = append(r.Updated, rc.Key()+" (Google -> local)")
			s.logf("UPDATE local %s (Google newer)", markdown.Filename(rc))

		case localMod && !googleNewer:
			freshETag := etagCache[lc.ResourceName]
			if !s.opts.DryRun {
				if _, err := s.google.UpdateWithCache(ctx, lc, freshETag); err != nil {
					if errors.Is(err, contacts.ErrNotFound) {
						r.Warnings = append(r.Warnings, lc.Key()+" - not found on server, will re-pull on next run")
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
			rc.Notes = mergeNotes(rc.Notes, lc.Notes)
			if !s.opts.DryRun {
				if err := s.store.WriteToPath(lc.FilePath, rc); err != nil {
					r.Errors = append(r.Errors, err)
					continue
				}
			}
			r.Updated = append(r.Updated, rc.Key()+" (conflict: Google wins)")
			r.Warnings = append(r.Warnings, lc.Key()+" - edited both locally and in Google; Google version kept")
			s.logf("CONFLICT %s (Google wins)", markdown.Filename(rc))

		default:
			r.Unchanged = append(r.Unchanged, rc.Key())
			s.logf("SKIP   %s", markdown.Filename(rc))
		}
	}

	// --- Push pass: local-only contacts -> Google ---
	createdThisRun := make(map[string]struct{})

	for _, lc := range local {
		if lc.ResourceName != "" {
			continue
		}
		s.logf("CREATE %q -> Google (FilePath=%s)", lc.Key(), lc.FilePath)
		if !s.opts.DryRun {
			if ok := s.createAndWriteBack(ctx, lc, r); !ok {
				continue
			}
			// Read back the file we just wrote to get the new resource_name.
			updated, err := s.store.Read(lc.FilePath)
			if err == nil {
				createdThisRun[updated.ResourceName] = struct{}{}
			}
			s.throttle(ctx)
		}
		r.Created = append(r.Created, lc.Key()+" (-> Google)")
	}

	for rn := range createdThisRun {
		remoteByResource[rn] = struct{}{}
	}

	if err := s.sweepOrphans(remoteByResource, r); err != nil {
		return nil, err
	}
	return r, nil
}

// sweepOrphans handles local files whose resource_name is not in Google.
// Files with a synced_hash (previously pulled successfully) are deleted when
// DeleteOrphans is set. Files without a synced_hash have their IDs cleared
// so they are re-created on the next sync.
func (s *Syncer) sweepOrphans(remoteByResource map[string]struct{}, r *Report) error {
	local, err := s.store.ReadAll()
	if err != nil {
		return fmt.Errorf("sweepOrphans: %w", err)
	}

	for _, lc := range local {
		if lc.ResourceName == "" {
			continue
		}
		if _, exists := remoteByResource[lc.ResourceName]; exists {
			continue
		}

		wasEverSynced := lc.SyncedHash != ""

		if s.opts.DeleteOrphans && wasEverSynced {
			if !s.opts.DryRun {
				if err := os.Remove(lc.FilePath); err != nil {
					r.Errors = append(r.Errors, fmt.Errorf("deleting orphan %q: %w", lc.FilePath, err))
					continue
				}
			}
			r.Deleted = append(r.Deleted, lc.Key())
			s.logf("DELETE %s (deleted on Google)", lc.FilePath)
		} else {
			lc.ResourceName = ""
			lc.ETag = ""
			lc.SyncedHash = ""
			if !s.opts.DryRun {
				if err := s.store.WriteToPath(lc.FilePath, lc); err != nil {
					r.Errors = append(r.Errors, fmt.Errorf("clearing stale IDs for %q: %w", lc.FilePath, err))
					continue
				}
			}
			msg := lc.Key() + " — not found in Google, IDs cleared; will re-create on next sync"
			if wasEverSynced {
				msg = lc.Key() + " — deleted in Google; run with --delete-orphans to remove local file"
			}
			r.Warnings = append(r.Warnings, msg)
			s.logf("WARN   %s", msg)
		}
	}
	return nil
}

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

func deduplicatePath(path, resourceName string) string {
	suffix := resourceName
	if idx := strings.LastIndexAny(resourceName, "/c"); idx >= 0 {
		suffix = resourceName[idx+1:]
	}
	if suffix == "" || suffix == resourceName {
		suffix = strings.ReplaceAll(resourceName, "/", "-")
	}
	if strings.HasSuffix(path, ".md") {
		return path[:len(path)-3] + "-" + suffix + ".md"
	}
	return path + "-" + suffix
}

func (s *Syncer) logf(format string, args ...any) {
	if s.opts.Verbose {
		fmt.Printf(format+"\n", args...)
	}
}
