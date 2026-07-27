// Package main demonstrates basic usage of the foundrydb-sdk-go.
//
// It creates a PostgreSQL service, waits for it to become running,
// retrieves the default user credentials, triggers a backup, and
// then cleans up by deleting the service.
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

	client := foundrydb.New(foundrydb.Config{
		APIURL:   apiURL,
		Username: username,
		Password: password,
	})

	ctx := context.Background()

	// List existing services.
	fmt.Println("Listing services...")
	services, err := client.ListServices(ctx)
	if err != nil {
		log.Fatalf("ListServices: %v", err)
	}
	fmt.Printf("Found %d existing service(s)\n", len(services))

	// Create a new PostgreSQL service.
	storageSizeGB := 50
	fmt.Println("\nCreating PostgreSQL service...")
	svc, err := client.CreateService(ctx, foundrydb.CreateServiceRequest{
		Name:          "sdk-basic-run",
		DatabaseType:  foundrydb.PostgreSQL,
		Version:       "17",
		PlanName:      "tier-2",
		Zone:          "se-sto1",
		StorageSizeGB: &storageSizeGB,
		StorageTier:   string(foundrydb.StorageTierMaxIOPS),
	})
	if err != nil {
		log.Fatalf("CreateService: %v", err)
	}
	fmt.Printf("Service created: id=%s status=%s\n", svc.ID, svc.Status)

	// Wait up to 15 minutes for the service to become running.
	fmt.Println("Waiting for service to become running (up to 20 minutes)...")
	svc, err = client.WaitForRunning(ctx, svc.ID, 20*time.Minute)
	if err != nil {
		log.Fatalf("WaitForRunning: %v", err)
	}
	fmt.Printf("Service is running: id=%s\n", svc.ID)

	// List database users.
	fmt.Println("\nListing database users...")
	users, err := client.ListUsers(ctx, svc.ID)
	if err != nil {
		log.Fatalf("ListUsers: %v", err)
	}
	fmt.Printf("Found %d user(s)\n", len(users))

	// Reveal credentials for the default user.
	if len(users) > 0 {
		creds, err := client.RevealPassword(ctx, svc.ID, users[0].Username)
		if err != nil {
			log.Fatalf("RevealPassword: %v", err)
		}
		fmt.Printf("Connection string: %s\n", creds.ConnectionString)
	}

	// Trigger an on-demand backup.
	fmt.Println("\nTriggering backup...")
	backup, err := client.TriggerBackup(ctx, svc.ID, foundrydb.CreateBackupRequest{
		BackupType: foundrydb.BackupTypeFull,
	})
	if err != nil {
		log.Fatalf("TriggerBackup: %v", err)
	}
	fmt.Printf("Backup started: id=%s status=%s\n", backup.ID, backup.Status)

	// Clean up.
	fmt.Println("\nDeleting service...")
	if err := client.DeleteService(ctx, svc.ID); err != nil {
		log.Fatalf("DeleteService: %v", err)
	}
	fmt.Println("Service deleted successfully.")
}
