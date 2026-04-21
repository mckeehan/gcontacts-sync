// Package markdown handles reading and writing Contact objects as Markdown files
// with YAML front-matter.
package markdown

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"gcontacts-sync/internal/model"

	"gopkg.in/yaml.v3"
)

var (
	unsafeChars = regexp.MustCompile(`[^\p{L}\p{N} .\-]`)
	multiSpace  = regexp.MustCompile(`\s+`)
	multiDash   = regexp.MustCompile(`-{2,}`)
)

// Store manages a directory of Markdown contact files.
type Store struct {
	dir string
}

// NewStore creates a Store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Dir returns the root directory.
func (s *Store) Dir() string { return s.dir }

// Filename returns the canonical filename for a contact (without directory).
func Filename(c *model.Contact) string {
	key := c.Key()
	safe := unsafeChars.ReplaceAllString(key, "")
	safe = multiSpace.ReplaceAllString(safe, " ")
	safe = strings.TrimSpace(safe)
	safe = strings.ReplaceAll(safe, " ", "-")
	safe = multiDash.ReplaceAllString(safe, "-")
	safe = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '.' {
			return r
		}
		return -1
	}, safe)
	safe = strings.Trim(safe, "-.")
	if safe == "" {
		safe = "unnamed"
	}
	return safe + ".md"
}

// Path returns the full path for a contact file.
func (s *Store) Path(c *model.Contact) string {
	return filepath.Join(s.dir, Filename(c))
}

// Write serialises a Contact to its Markdown file (YAML front-matter + body).
func (s *Store) Write(c *model.Contact) error {
	data, err := Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path(c), data, 0o644)
}

// Read parses a Markdown file at path into a Contact.
func (s *Store) Read(path string) (*model.Contact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Unmarshal(data)
}

// ListPaths returns all .md file paths in the store directory.
func (s *Store) ListPaths() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			paths = append(paths, filepath.Join(s.dir, e.Name()))
		}
	}
	return paths, nil
}

// ReadAll returns all contacts found in the store directory.
func (s *Store) ReadAll() ([]*model.Contact, error) {
	paths, err := s.ListPaths()
	if err != nil {
		return nil, err
	}
	var contacts []*model.Contact
	for _, p := range paths {
		c, err := s.Read(p)
		if err != nil {
			return nil, fmt.Errorf("reading %q: %w", p, err)
		}
		contacts = append(contacts, c)
	}
	return contacts, nil
}

// Delete removes the markdown file for a contact.
func (s *Store) Delete(c *model.Contact) error {
	return os.Remove(s.Path(c))
}

// IsLocallyModified returns true if the contact's content has changed since it
// was last written by a pull from Google.
//
// It works by comparing a SHA-256 hash of all user-editable fields (stored in
// the synced_hash front-matter key on every write) against a freshly computed
// hash of the current file content. If the hashes match, the user has not
// touched the file. If they differ — or if synced_hash is absent (old files) —
// the file is treated as locally modified.
func IsLocallyModified(c *model.Contact) bool {
	if c.SyncedHash == "" {
		// No hash recorded: file predates this feature; treat as unmodified to
		// avoid a mass spurious push on first run after upgrade.
		return false
	}
	return contentHash(c) != c.SyncedHash
}

// contentHash produces a stable SHA-256 of all user-editable contact fields
// and the notes body. It encodes via YAML (the same codec used for storage)
// so the hash is guaranteed to be stable across a write → read round-trip.
// Sync-only metadata fields (resource_name, etag, synced_hash, timestamps)
// are intentionally excluded.
func contentHash(c *model.Contact) string {
	type hashable struct {
		DisplayName    string               `yaml:"display_name,omitempty"`
		GivenName      string               `yaml:"given_name,omitempty"`
		MiddleName     string               `yaml:"middle_name,omitempty"`
		FamilyName     string               `yaml:"family_name,omitempty"`
		Prefix         string               `yaml:"name_prefix,omitempty"`
		Suffix         string               `yaml:"name_suffix,omitempty"`
		Nickname       string               `yaml:"nickname,omitempty"`
		Company        string               `yaml:"company,omitempty"`
		JobTitle       string               `yaml:"job_title,omitempty"`
		Department     string               `yaml:"department,omitempty"`
		Emails         []model.LabeledValue `yaml:"emails,omitempty"`
		PhoneNumbers   []model.LabeledValue `yaml:"phones,omitempty"`
		Addresses      []model.Address      `yaml:"addresses,omitempty"`
		URLs           []model.LabeledValue `yaml:"urls,omitempty"`
		SocialProfiles []model.LabeledValue `yaml:"social_profiles,omitempty"`
		IMs            []model.LabeledValue `yaml:"ims,omitempty"`
		Birthday       string               `yaml:"birthday,omitempty"`
		Events         []model.LabeledValue `yaml:"events,omitempty"`
		Relations      []model.LabeledValue `yaml:"relations,omitempty"`
		Groups         []string             `yaml:"groups,omitempty"`
		Notes          string               `yaml:"notes,omitempty"`
	}
	h := hashable{
		DisplayName:    c.DisplayName,
		GivenName:      c.GivenName,
		MiddleName:     c.MiddleName,
		FamilyName:     c.FamilyName,
		Prefix:         c.Prefix,
		Suffix:         c.Suffix,
		Nickname:       c.Nickname,
		Company:        c.Company,
		JobTitle:       c.JobTitle,
		Department:     c.Department,
		Emails:         c.Emails,
		PhoneNumbers:   c.PhoneNumbers,
		Addresses:      c.Addresses,
		URLs:           c.URLs,
		SocialProfiles: c.SocialProfiles,
		IMs:            c.IMs,
		Birthday:       c.Birthday,
		Events:         c.Events,
		Relations:      c.Relations,
		Groups:         c.Groups,
		Notes:          c.Notes, // already trimmed by Marshal before this is called
	}
	b, _ := yaml.Marshal(h)
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

// ---------- serialisation ----------

type frontMatter struct {
	ResourceName string `yaml:"resource_name,omitempty"`
	ETag         string `yaml:"etag,omitempty"`
	// SyncedHash is the content hash at the time of the last pull from Google.
	// It is used to detect local edits without relying on filesystem mtimes.
	SyncedHash string `yaml:"synced_hash,omitempty"`

	DisplayName string `yaml:"display_name,omitempty"`
	GivenName   string `yaml:"given_name,omitempty"`
	MiddleName  string `yaml:"middle_name,omitempty"`
	FamilyName  string `yaml:"family_name,omitempty"`
	Prefix      string `yaml:"name_prefix,omitempty"`
	Suffix      string `yaml:"name_suffix,omitempty"`
	Nickname    string `yaml:"nickname,omitempty"`

	Company    string `yaml:"company,omitempty"`
	JobTitle   string `yaml:"job_title,omitempty"`
	Department string `yaml:"department,omitempty"`

	Emails       []model.LabeledValue `yaml:"emails,omitempty"`
	PhoneNumbers []model.LabeledValue `yaml:"phones,omitempty"`
	Addresses    []model.Address      `yaml:"addresses,omitempty"`

	URLs           []model.LabeledValue `yaml:"urls,omitempty"`
	SocialProfiles []model.LabeledValue `yaml:"social_profiles,omitempty"`
	IMs            []model.LabeledValue `yaml:"ims,omitempty"`

	Birthday string               `yaml:"birthday,omitempty"`
	Events   []model.LabeledValue `yaml:"events,omitempty"`

	Relations []model.LabeledValue `yaml:"relations,omitempty"`
	Groups    []string             `yaml:"groups,omitempty"`

	CreatedAt string `yaml:"created_at,omitempty"`
	UpdatedAt string `yaml:"updated_at,omitempty"`
}

func toFrontMatter(c *model.Contact) frontMatter {
	fm := frontMatter{
		ResourceName:   c.ResourceName,
		ETag:           c.ETag,
		SyncedHash:     c.SyncedHash,
		DisplayName:    c.DisplayName,
		GivenName:      c.GivenName,
		MiddleName:     c.MiddleName,
		FamilyName:     c.FamilyName,
		Prefix:         c.Prefix,
		Suffix:         c.Suffix,
		Nickname:       c.Nickname,
		Company:        c.Company,
		JobTitle:       c.JobTitle,
		Department:     c.Department,
		Emails:         c.Emails,
		PhoneNumbers:   c.PhoneNumbers,
		Addresses:      c.Addresses,
		URLs:           c.URLs,
		SocialProfiles: c.SocialProfiles,
		IMs:            c.IMs,
		Birthday:       c.Birthday,
		Events:         c.Events,
		Relations:      c.Relations,
		Groups:         c.Groups,
	}
	if !c.CreatedAt.IsZero() {
		fm.CreatedAt = c.CreatedAt.Format("2006-01-02T15:04:05Z")
	}
	if !c.UpdatedAt.IsZero() {
		fm.UpdatedAt = c.UpdatedAt.Format("2006-01-02T15:04:05Z")
	}
	return fm
}

func fromFrontMatter(fm frontMatter) *model.Contact {
	c := &model.Contact{
		ResourceName:   fm.ResourceName,
		ETag:           fm.ETag,
		SyncedHash:     fm.SyncedHash,
		DisplayName:    fm.DisplayName,
		GivenName:      fm.GivenName,
		MiddleName:     fm.MiddleName,
		FamilyName:     fm.FamilyName,
		Prefix:         fm.Prefix,
		Suffix:         fm.Suffix,
		Nickname:       fm.Nickname,
		Company:        fm.Company,
		JobTitle:       fm.JobTitle,
		Department:     fm.Department,
		Emails:         fm.Emails,
		PhoneNumbers:   fm.PhoneNumbers,
		Addresses:      fm.Addresses,
		URLs:           fm.URLs,
		SocialProfiles: fm.SocialProfiles,
		IMs:            fm.IMs,
		Birthday:       fm.Birthday,
		Events:         fm.Events,
		Relations:      fm.Relations,
		Groups:         fm.Groups,
	}
	c.UpdatedAt, _ = time.Parse("2006-01-02T15:04:05Z", fm.UpdatedAt)
	c.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", fm.CreatedAt)
	return c
}

// Marshal converts a Contact into Markdown bytes (YAML front-matter + notes body).
// It always computes and stores a fresh synced_hash so subsequent runs can detect
// whether the user has edited the file.
func Marshal(c *model.Contact) ([]byte, error) {
	// Normalise notes the same way Unmarshal will read them back, so the hash
	// is stable across a write→read round-trip.
	c.Notes = strings.TrimSpace(c.Notes)

	fm := toFrontMatter(c)
	fm.SyncedHash = contentHash(c) // hash of the normalised content

	var buf bytes.Buffer
	buf.WriteString("---\n")

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return nil, fmt.Errorf("encoding YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	buf.WriteString("---\n")

	if c.Notes != "" {
		buf.WriteString("\n")
		buf.WriteString(c.Notes)
		buf.WriteString("\n")
	}

	return buf.Bytes(), nil
}

// Unmarshal parses Markdown bytes (YAML front-matter + body) into a Contact.
func Unmarshal(data []byte) (*model.Contact, error) {
	s := string(data)

	if !strings.HasPrefix(s, "---") {
		return nil, fmt.Errorf("missing YAML front-matter (expected leading '---')")
	}

	rest := s[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil, fmt.Errorf("unclosed YAML front-matter")
	}

	yamlPart := rest[:idx]
	body := strings.TrimPrefix(rest[idx+4:], "\n")

	var fm frontMatter
	if err := yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	c := fromFrontMatter(fm)
	c.Notes = strings.TrimSpace(body)
	return c, nil
}
