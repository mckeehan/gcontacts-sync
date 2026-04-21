package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"gcontacts-sync/internal/auth"
	"gcontacts-sync/internal/contacts"
	"gcontacts-sync/internal/markdown"
	"gcontacts-sync/internal/sync"
)

func main() {
	var (
		dir           = flag.String("dir", "./contacts", "Directory to store markdown files")
		credFile      = flag.String("credentials", "credentials.json", "Path to OAuth2 credentials JSON")
		tokenFile     = flag.String("token", "token.json", "Path to store/load OAuth2 token")
		dryRun        = flag.Bool("dry-run", false, "Preview changes without writing files")
		pullOnly      = flag.Bool("pull", false, "Only pull from Google Contacts → Markdown (default: bidirectional)")
		pushOnly      = flag.Bool("push", false, "Only push from Markdown → Google Contacts")
		verbose       = flag.Bool("verbose", false, "Verbose logging")
		deleteOrphans = flag.Bool("delete-orphans", false, "Delete local files for contacts deleted in Google")
	)
	flag.Parse()

	if *pullOnly && *pushOnly {
		log.Fatal("Cannot use both --pull and --push; omit both for bidirectional sync")
	}

	ctx := context.Background()

	// Authenticate
	client, err := auth.NewOAuthClient(ctx, *credFile, *tokenFile)
	if err != nil {
		log.Fatalf("Auth error: %v\n\nRun with valid credentials.json downloaded from Google Cloud Console.\nSee README.md for setup instructions.", err)
	}

	// Build services
	contactSvc, err := contacts.NewService(ctx, client)
	if err != nil {
		log.Fatalf("Failed to create contacts service: %v", err)
	}

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatalf("Cannot create directory %q: %v", *dir, err)
	}

	mdStore := markdown.NewStore(*dir)
	syncer := sync.NewSyncer(contactSvc, mdStore, sync.Options{
		DryRun:        *dryRun,
		Verbose:       *verbose,
		DeleteOrphans: *deleteOrphans,
	})

	switch {
	case *pullOnly:
		fmt.Println("→ Pulling Google Contacts → Markdown…")
		report, err := syncer.Pull(ctx)
		if err != nil {
			log.Fatalf("Pull failed: %v", err)
		}
		report.Print()

	case *pushOnly:
		fmt.Println("→ Pushing Markdown → Google Contacts…")
		report, err := syncer.Push(ctx)
		if err != nil {
			log.Fatalf("Push failed: %v", err)
		}
		report.Print()

	default:
		fmt.Println("⇄  Bidirectional sync…")
		report, err := syncer.Sync(ctx)
		if err != nil {
			log.Fatalf("Sync failed: %v", err)
		}
		report.Print()
	}
}
