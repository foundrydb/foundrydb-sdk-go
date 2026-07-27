// Package main demonstrates multi-organization usage of the foundrydb-sdk-go.
//
// It lists all organizations the authenticated user belongs to and creates
// a Valkey service scoped to the first non-personal organization, using the
// client-level OrgID to scope all requests automatically.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/foundrydb/foundrydb-sdk-go/foundrydb"
)

func main() {
	username := os.Getenv("FOUNDRYDB_USERNAME")
	password := os.Getenv("FOUNDRYDB_PASSWORD")
	apiURL := os.Getenv("FOUNDRYDB_API_URL")
	if apiURL == "" {
		apiURL = "https://api.foundrydb.com"
	}
	if username == "" || password == "" {
		log.Fatal("FOUNDRYDB_USERNAME and FOUNDRYDB_PASSWORD must be set")
	}

	// Create a base client without org scoping to list organizations first.
	baseClient := foundrydb.New(foundrydb.Config{
		APIURL:   apiURL,
		Username: username,
		Password: password,
	})

	ctx := context.Background()

	// List all organizations.
	fmt.Println("Listing organizations...")
	orgs, err := baseClient.ListOrganizations(ctx)
	if err != nil {
		log.Fatalf("ListOrganizations: %v", err)
	}
	fmt.Printf("Found %d organization(s):\n", len(orgs))
	for _, org := range orgs {
		fmt.Printf("  - %s (id=%s, slug=%s, personal=%v)\n",
			org.Name, org.ID, org.Slug, org.IsPersonal)
	}

	// Find the first team organization (non-personal).
	var targetOrgID string
	for _, org := range orgs {
		if !org.IsPersonal {
			targetOrgID = org.ID
			fmt.Printf("\nUsing organization: %s (%s)\n", org.Name, org.ID)
			break
		}
	}
	if targetOrgID == "" {
		// Fall back to the personal org if no team org was found.
		if len(orgs) > 0 {
			targetOrgID = orgs[0].ID
			fmt.Printf("\nNo team org found; using personal org: %s\n", orgs[0].ID)
		} else {
			log.Fatal("No organizations found for this user")
		}
	}

	// Create an org-scoped client. All subsequent API calls will include
	// X-Active-Org-ID automatically.
	orgClient := foundrydb.New(foundrydb.Config{
		APIURL:   apiURL,
		Username: username,
		Password: password,
		OrgID:    targetOrgID,
	})

	// List services scoped to the target org.
	services, err := orgClient.ListServices(ctx)
	if err != nil {
		log.Fatalf("ListServices: %v", err)
	}
	fmt.Printf("Services in org: %d\n", len(services))

	// Create a Valkey service scoped to the target organization.
	storageSizeGB := 25
	fmt.Println("\nCreating Valkey service in the target organization...")
	svc, err := orgClient.CreateService(ctx, foundrydb.CreateServiceRequest{
		Name:          "sdk-multi-org-valkey",
		DatabaseType:  foundrydb.Valkey,
		Version:       "8.1",
		PlanName:      "tier-2",
		Zone:          "se-sto1",
		StorageSizeGB: &storageSizeGB,
		StorageTier:   string(foundrydb.StorageTierMaxIOPS),
	})
	if err != nil {
		log.Fatalf("CreateService: %v", err)
	}
	fmt.Printf("Service created: id=%s status=%s\n", svc.ID, svc.Status)

	// Wait for the service to become running.
	fmt.Println("Waiting for service to become running (up to 15 minutes)...")
	svc, err = orgClient.WaitForRunning(ctx, svc.ID, 15*time.Minute)
	if err != nil {
		log.Fatalf("WaitForRunning: %v", err)
	}
	fmt.Printf("Service is running: id=%s\n", svc.ID)
	if len(svc.DNSRecords) > 0 {
		fmt.Printf("DNS: %s\n", svc.DNSRecords[0].FullDomain)
	}

	// Print list backups.
	backups, err := orgClient.ListBackups(ctx, svc.ID)
	if err != nil {
		log.Fatalf("ListBackups: %v", err)
	}
	fmt.Printf("Backups: %d\n", len(backups))

	// Clean up.
	fmt.Println("\nCleaning up service...")
	if err := orgClient.DeleteService(ctx, svc.ID); err != nil {
		log.Fatalf("DeleteService: %v", err)
	}
	fmt.Println("Done.")
}
