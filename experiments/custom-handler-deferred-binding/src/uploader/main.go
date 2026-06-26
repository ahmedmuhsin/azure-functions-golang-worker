package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

// Uploads a known blob into the storage account named by AzureWebJobsStorage so
// the deferred-binding test has data, resolving the connection the same way a
// function app does.
func main() {
	container := "test-container"
	name := "hello.txt"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	content := []byte("THIS-IS-THE-FULL-BLOB-CONTENT-PAYLOAD")

	connStr, err := storageConnString()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	client, err := azblob.NewClientFromConnectionString(connStr, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "client:", err)
		os.Exit(1)
	}
	ctx := context.Background()
	_, _ = client.CreateContainer(ctx, container, nil)
	_, err = client.UploadBuffer(ctx, container, name, content, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "upload:", err)
		os.Exit(1)
	}
	fmt.Printf("uploaded %s/%s (%d bytes)\n", container, name, len(content))
}

// storageConnString resolves AzureWebJobsStorage the way the host does: prefer
// the environment, then fall back to local.settings.json in the current
// directory.
func storageConnString() (string, error) {
	if v := os.Getenv("AzureWebJobsStorage"); v != "" {
		return v, nil
	}
	data, err := os.ReadFile("local.settings.json")
	if err != nil {
		return "", fmt.Errorf("AzureWebJobsStorage not set and local.settings.json not readable: %w", err)
	}
	var s struct {
		Values map[string]string `json:"Values"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return "", fmt.Errorf("parse local.settings.json: %w", err)
	}
	if v := s.Values["AzureWebJobsStorage"]; v != "" {
		return v, nil
	}
	return "", fmt.Errorf("AzureWebJobsStorage not found in environment or local.settings.json")
}
