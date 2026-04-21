// Package model defines the canonical Contact type used throughout the sync pipeline.
package model

import "time"

// Contact is the internal representation of a Google Contact.
// All fields map 1-to-1 with People API fields and with YAML front-matter keys.
type Contact struct {
	// --- Identity ---
	ResourceName string `yaml:"resource_name,omitempty"` // e.g. "people/c123456"
	ETag         string `yaml:"etag,omitempty"`

	// --- Name ---
	DisplayName string `yaml:"display_name,omitempty"`
	GivenName   string `yaml:"given_name,omitempty"`
	MiddleName  string `yaml:"middle_name,omitempty"`
	FamilyName  string `yaml:"family_name,omitempty"`
	Prefix      string `yaml:"name_prefix,omitempty"`
	Suffix      string `yaml:"name_suffix,omitempty"`
	Nickname    string `yaml:"nickname,omitempty"`

	// --- Organisation ---
	Company    string `yaml:"company,omitempty"`
	JobTitle   string `yaml:"job_title,omitempty"`
	Department string `yaml:"department,omitempty"`

	// --- Contact info ---
	Emails       []LabeledValue `yaml:"emails,omitempty"`
	PhoneNumbers []LabeledValue `yaml:"phones,omitempty"`

	// --- Addresses ---
	Addresses []Address `yaml:"addresses,omitempty"`

	// --- Online ---
	URLs       []LabeledValue `yaml:"urls,omitempty"`
	SocialProfiles []LabeledValue `yaml:"social_profiles,omitempty"`
	IMs        []LabeledValue `yaml:"ims,omitempty"`

	// --- Dates ---
	Birthday  string `yaml:"birthday,omitempty"` // YYYY-MM-DD or --MM-DD
	Events    []LabeledValue `yaml:"events,omitempty"`

	// --- Relations ---
	Relations []LabeledValue `yaml:"relations,omitempty"`

	// --- Groups / Labels ---
	Groups []string `yaml:"groups,omitempty"`

	// --- Metadata ---
	CreatedAt time.Time `yaml:"created_at,omitempty"`
	UpdatedAt time.Time `yaml:"updated_at,omitempty"`

	// --- Sync metadata ---
	// SyncedHash is the SHA-256 of content fields at the time of the last pull.
	// Used to detect local edits without relying on filesystem mtimes.
	SyncedHash string `yaml:"synced_hash,omitempty"`

	// --- Notes (body of the markdown file) ---
	Notes string `yaml:"-"`
}

// LabeledValue is a value with an associated label (e.g. "work", "home", "mobile").
type LabeledValue struct {
	Label string `yaml:"label,omitempty"`
	Value string `yaml:"value"`
}

// Address holds a structured postal address.
type Address struct {
	Label          string `yaml:"label,omitempty"`
	Street         string `yaml:"street,omitempty"`
	City           string `yaml:"city,omitempty"`
	Region         string `yaml:"region,omitempty"`
	PostalCode     string `yaml:"postal_code,omitempty"`
	Country        string `yaml:"country,omitempty"`
	CountryCode    string `yaml:"country_code,omitempty"`
	FormattedValue string `yaml:"formatted,omitempty"`
}

// Key returns a stable, human-readable key for the contact used as the filename stem.
// Priority: DisplayName → Company → ResourceName.
func (c *Contact) Key() string {
	switch {
	case c.DisplayName != "":
		return c.DisplayName
	case c.Company != "":
		return c.Company
	default:
		return c.ResourceName
	}
}
