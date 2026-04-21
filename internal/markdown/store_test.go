package markdown_test

import (
	"strings"
	"testing"
	"time"

	"gcontacts-sync/internal/markdown"
	"gcontacts-sync/internal/model"
)

func sampleContact() *model.Contact {
	return &model.Contact{
		ResourceName: "people/c123456789",
		ETag:         "abc123",
		DisplayName:  "Jane Ó'Connell",
		GivenName:    "Jane",
		FamilyName:   "O'Connell",
		Nickname:     "Janey",
		Company:      "Acme Corp",
		JobTitle:     "VP Engineering",
		Department:   "Platform",
		Emails: []model.LabeledValue{
			{Label: "work", Value: "jane@acme.com"},
			{Label: "home", Value: "jane@personal.com"},
		},
		PhoneNumbers: []model.LabeledValue{
			{Label: "mobile", Value: "+1-555-123-4567"},
		},
		Addresses: []model.Address{
			{
				Label:      "work",
				Street:     "123 Main St",
				City:       "Springfield",
				Region:     "IL",
				PostalCode: "62701",
				Country:    "USA",
			},
		},
		URLs: []model.LabeledValue{
			{Label: "homepage", Value: "https://jane.example.com"},
		},
		Birthday:  "1990-03-15",
		Groups:    []string{"contactGroups/starred"},
		UpdatedAt: time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
		Notes:     "Met at GopherCon 2023.\n\nGreat speaker, follow up about open source project.",
	}
}

func TestMarshalUnmarshal_roundtrip(t *testing.T) {
	c := sampleContact()

	data, err := markdown.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := markdown.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	assertEqual(t, "ResourceName", c.ResourceName, got.ResourceName)
	assertEqual(t, "DisplayName", c.DisplayName, got.DisplayName)
	assertEqual(t, "GivenName", c.GivenName, got.GivenName)
	assertEqual(t, "FamilyName", c.FamilyName, got.FamilyName)
	assertEqual(t, "Nickname", c.Nickname, got.Nickname)
	assertEqual(t, "Company", c.Company, got.Company)
	assertEqual(t, "JobTitle", c.JobTitle, got.JobTitle)
	assertEqual(t, "Birthday", c.Birthday, got.Birthday)
	assertEqual(t, "Notes", c.Notes, got.Notes)

	if len(got.Emails) != 2 {
		t.Errorf("Emails: want 2, got %d", len(got.Emails))
	}
	if len(got.PhoneNumbers) != 1 {
		t.Errorf("PhoneNumbers: want 1, got %d", len(got.PhoneNumbers))
	}
	if len(got.Addresses) != 1 {
		t.Errorf("Addresses: want 1, got %d", len(got.Addresses))
	}
}

func TestMarshal_format(t *testing.T) {
	c := sampleContact()
	data, err := markdown.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	s := string(data)

	if !strings.HasPrefix(s, "---\n") {
		t.Error("should start with ---")
	}
	if !strings.Contains(s, "display_name: Jane") {
		t.Error("should contain display_name field")
	}
	if !strings.Contains(s, "Met at GopherCon") {
		t.Error("should contain notes body")
	}
}

func TestFilename(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want string
	}{
		{"normal", "Jane Smith", "Jane-Smith.md"},
		{"special chars", "Ó'Brian, Seán", "Brian-Sen.md"},
		{"company with dot", "Acme Corp. Ltd.", "Acme-Corp.-Ltd.md"},
		{"emoji stripped", "Bob 🚀 Jones", "Bob-Jones.md"},
		{"slashes stripped", "AC/DC", "ACDC.md"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &model.Contact{DisplayName: tc.key}
			got := markdown.Filename(c)
			if got != tc.want {
				t.Errorf("Filename(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

func assertEqual(t *testing.T, field, want, got string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %q, got %q", field, want, got)
	}
}
